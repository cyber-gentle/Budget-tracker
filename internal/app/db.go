package app

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

// DBStore wraps the SQL connection to Turso / SQLite.
type DBStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewDBStore connects to Turso (if URL provided) or falls back to local SQLite.
func NewDBStore(rawURL, authToken string) (*DBStore, error) {
	connStr := rawURL
	if connStr == "" {
		connStr = os.Getenv("TURSO_DATABASE_URL")
	}
	if authToken == "" {
		authToken = os.Getenv("TURSO_AUTH_TOKEN")
	}

	driverName := "sqlite"
	if connStr == "" {
		// Default to local SQLite file for development
		connStr = "file:spendly.db"
	} else if strings.HasPrefix(connStr, "libsql://") || strings.HasPrefix(connStr, "http://") || strings.HasPrefix(connStr, "https://") || strings.HasPrefix(connStr, "ws://") || strings.HasPrefix(connStr, "wss://") {
		driverName = "libsql"
		if authToken != "" && !strings.Contains(connStr, "authToken=") {
			u, err := url.Parse(connStr)
			if err == nil {
				q := u.Query()
				q.Set("authToken", authToken)
				u.RawQuery = q.Encode()
				connStr = u.String()
			} else {
				connStr = fmt.Sprintf("%s?authToken=%s", connStr, authToken)
			}
		}
	}

	db, err := sql.Open(driverName, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &DBStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return store, nil
}

func (s *DBStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		username TEXT PRIMARY KEY,
		password TEXT NOT NULL,
		currency TEXT DEFAULT '₦',
		created_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		expires_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		amount REAL NOT NULL,
		category TEXT NOT NULL,
		note TEXT,
		date DATETIME,
		type TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS budgets (
		username TEXT NOT NULL,
		category TEXT NOT NULL,
		monthly_limit REAL NOT NULL,
		PRIMARY KEY (username, category)
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// ─── Authentication & User Methods ──────────────────────────────────────────

func (s *DBStore) Signup(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}

	var exists int
	err := s.db.QueryRow("SELECT COUNT(1) FROM users WHERE username = ?", username).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("username already taken")
	}

	hash, err := createPasswordHash(password)
	if err != nil {
		return err
	}

	_, err = s.db.Exec("INSERT INTO users (username, password, currency, created_at) VALUES (?, ?, '₦', ?)",
		username, hash, time.Now())
	return err
}

func (s *DBStore) Login(username, password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var storedHash string
	err := s.db.QueryRow("SELECT password FROM users WHERE username = ?", username).Scan(&storedHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("invalid username or password")
		}
		return "", err
	}

	matched, needsUpgrade := verifyPassword(storedHash, password)
	if !matched {
		return "", fmt.Errorf("invalid username or password")
	}

	if needsUpgrade {
		if newHash, err := createPasswordHash(password); err == nil {
			_, _ = s.db.Exec("UPDATE users SET password = ? WHERE username = ?", newHash, username)
		}
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}

	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	_, err = s.db.Exec("INSERT INTO sessions (token, username, expires_at) VALUES (?, ?, ?)", token, username, expiresAt)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (s *DBStore) ValidateSession(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if token == "" {
		return "", false
	}

	var username string
	var expiresAt time.Time
	err := s.db.QueryRow("SELECT username, expires_at FROM sessions WHERE token = ?", token).Scan(&username, &expiresAt)
	if err != nil {
		return "", false
	}

	if time.Now().After(expiresAt) {
		go func(t string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			_, _ = s.db.Exec("DELETE FROM sessions WHERE token = ?", t)
		}(token)
		return "", false
	}

	return username, true
}

func (s *DBStore) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec("DELETE FROM sessions WHERE token = ?", token)
}

func (s *DBStore) GetCurrency(username string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var curr string
	err := s.db.QueryRow("SELECT currency FROM users WHERE username = ?", username).Scan(&curr)
	if err != nil || curr == "" {
		return "₦"
	}
	return curr
}

func (s *DBStore) SetCurrency(username, currency string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if currency == "" {
		currency = "₦"
	}
	_, err := s.db.Exec("UPDATE users SET currency = ? WHERE username = ?", currency, username)
	return err
}

// ─── Transaction Methods ───────────────────────────────────────────────────

func (s *DBStore) AddTransaction(username string, amount float64, category, note, txnType string, optDate ...time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	txDate := time.Now()
	if len(optDate) > 0 && !optDate[0].IsZero() {
		txDate = optDate[0]
	}

	_, err := s.db.Exec(
		"INSERT INTO transactions (username, amount, category, note, date, type) VALUES (?, ?, ?, ?, ?, ?)",
		username, amount, category, note, txDate, txnType,
	)
	return err
}

func (s *DBStore) UpdateTransaction(id int, username string, amount float64, category, note, txnType string, optDate ...time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res sql.Result
	var err error

	if len(optDate) > 0 && !optDate[0].IsZero() {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ?, date = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, optDate[0], id, username,
		)
	} else {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, id, username,
		)
	}

	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) DeleteTransaction(id int, username string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec("DELETE FROM transactions WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) GetTransactions(username string) ([]Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, amount, category, note, date, type FROM transactions WHERE username = ? ORDER BY date DESC, id DESC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txs []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Amount, &t.Category, &t.Note, &t.Date, &t.Type); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}
		txs = append(txs, t)
	}
	return txs, nil
}

// ─── Budget Methods ────────────────────────────────────────────────────────

func (s *DBStore) GetBudgets(username string) (map[string]float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT category, monthly_limit FROM budgets WHERE username = ?", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	budgets := make(map[string]float64)
	for rows.Next() {
		var cat string
		var limit float64
		if err := rows.Scan(&cat, &limit); err == nil {
			budgets[cat] = limit
		}
	}
	return budgets, nil
}

func (s *DBStore) SetBudget(username, category string, limit float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 {
		_, err := s.db.Exec("DELETE FROM budgets WHERE username = ? AND category = ?", username, category)
		return err
	}

	_, err := s.db.Exec(`
		INSERT INTO budgets (username, category, monthly_limit) VALUES (?, ?, ?)
		ON CONFLICT(username, category) DO UPDATE SET monthly_limit = excluded.monthly_limit
	`, username, category, limit)
	return err
}

// ─── Financial Calculations & Analytics ────────────────────────────────────

func (s *DBStore) CalculateTotals(username string) (income, expense, balance float64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT type, COALESCE(SUM(amount), 0) FROM transactions WHERE username = ? GROUP BY type", username)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var t string
		var total float64
		if err := rows.Scan(&t, &total); err == nil {
			if t == "income" {
				income = total
			} else if t == "expense" {
				expense = total
			}
		}
	}
	balance = income - expense
	return income, expense, balance, nil
}

func (s *DBStore) GetCurrentMonthSpending(username string) (map[string]float64, error) {
	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	res := make(map[string]float64)
	for _, t := range txs {
		if t.Type == "expense" && t.Date.Year() == now.Year() && t.Date.Month() == now.Month() {
			res[t.Category] += t.Amount
		}
	}
	return res, nil
}

func (s *DBStore) GetMonthlyTrends(username string, monthsBack int) ([]MonthlySummary, error) {
	if monthsBack <= 0 {
		monthsBack = 6
	}

	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	type monthKey struct {
		year  int
		month time.Month
	}
	keys := make([]monthKey, monthsBack)
	for i := 0; i < monthsBack; i++ {
		d := time.Date(now.Year(), now.Month()-time.Month(monthsBack-1-i), 1, 0, 0, 0, 0, time.UTC)
		keys[i] = monthKey{year: d.Year(), month: d.Month()}
	}

	sums := make(map[monthKey]*MonthlySummary)
	for _, k := range keys {
		label := fmt.Sprintf("%s %02d", k.month.String()[:3], k.year%100)
		sums[k] = &MonthlySummary{Month: label}
	}

	for _, t := range txs {
		k := monthKey{year: t.Date.Year(), month: t.Date.Month()}
		if s, ok := sums[k]; ok {
			if t.Type == "income" {
				s.Income += t.Amount
			} else if t.Type == "expense" {
				s.Expense += t.Amount
			}
		}
	}

	result := make([]MonthlySummary, len(keys))
	for i, k := range keys {
		result[i] = *sums[k]
	}
	return result, nil
}

func (s *DBStore) CategoryBreakdown(username string, allTime bool) (map[string]float64, error) {
	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	res := make(map[string]float64)
	for _, t := range txs {
		if t.Type != "expense" {
			continue
		}
		if allTime || (t.Date.Year() == now.Year() && t.Date.Month() == now.Month()) {
			res[t.Category] += t.Amount
		}
	}
	return res, nil
}
