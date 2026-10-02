package app

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
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
		// Use /tmp in serverless/Vercel environments where root is read-only
		if os.Getenv("VERCEL") != "" || os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
			connStr = "file:/tmp/spendly.db"
		} else {
			connStr = "file:spendly.db"
		}
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
		full_name TEXT DEFAULT '',
		email TEXT,
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

	CREATE TABLE IF NOT EXISTS debts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		person_name TEXT NOT NULL,
		amount REAL NOT NULL,
		amount_paid REAL NOT NULL DEFAULT 0,
		type TEXT NOT NULL,
		due_date DATETIME,
		note TEXT,
		status TEXT NOT NULL DEFAULT 'unpaid',
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_debts_user ON debts(username);

	CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		name TEXT NOT NULL,
		amount REAL NOT NULL,
		category TEXT NOT NULL,
		billing_cycle TEXT NOT NULL DEFAULT 'monthly',
		next_due_date DATETIME NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_subscriptions_user ON subscriptions(username);

	CREATE TABLE IF NOT EXISTS goals (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		name TEXT NOT NULL,
		target_amount REAL NOT NULL,
		saved_amount REAL NOT NULL DEFAULT 0,
		category TEXT NOT NULL DEFAULT 'savings',
		target_date DATETIME,
		color TEXT DEFAULT '#10B981',
		emoji TEXT DEFAULT '🎯',
		status TEXT NOT NULL DEFAULT 'in_progress',
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_goals_user ON goals(username);

	CREATE TABLE IF NOT EXISTS goal_contributions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		goal_id INTEGER NOT NULL,
		username TEXT NOT NULL,
		amount REAL NOT NULL,
		type TEXT NOT NULL,
		note TEXT,
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_contributions_goal ON goal_contributions(goal_id);
	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		name TEXT NOT NULL,
		slug TEXT NOT NULL,
		type TEXT NOT NULL,
		color TEXT DEFAULT '#10B981',
		emoji TEXT DEFAULT '🏷️',
		is_default BOOLEAN DEFAULT 0,
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_categories_user ON categories(username);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Migrate existing tables if columns don't exist
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN email TEXT")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN full_name TEXT DEFAULT ''")
	_, _ = s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email)")
	_, _ = s.db.Exec("ALTER TABLE transactions ADD COLUMN tags TEXT DEFAULT ''")

	return nil
}

// ─── Authentication & User Methods ──────────────────────────────────────────

func (s *DBStore) Signup(username, email, password string, optFullName ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	fullName := ""
	if len(optFullName) > 0 {
		fullName = strings.TrimSpace(optFullName[0])
	}
	if username == "" || email == "" || password == "" {
		return fmt.Errorf("username, email, and password are required")
	}

	if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		return fmt.Errorf("please enter a valid email address")
	}

	var exists int
	err := s.db.QueryRow("SELECT COUNT(1) FROM users WHERE LOWER(username) = LOWER(?)", username).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("username already taken")
	}

	var emailExists int
	err = s.db.QueryRow("SELECT COUNT(1) FROM users WHERE LOWER(email) = LOWER(?)", email).Scan(&emailExists)
	if err != nil {
		return err
	}
	if emailExists > 0 {
		return fmt.Errorf("email address already in use")
	}

	hash, err := createPasswordHash(password)
	if err != nil {
		return err
	}

	_, err = s.db.Exec("INSERT INTO users (username, full_name, email, password, currency, created_at) VALUES (?, ?, ?, ?, '₦', ?)",
		username, fullName, email, hash, time.Now())
	return err
}

func (s *DBStore) Login(identifier, password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	identifier = strings.TrimSpace(identifier)
	if identifier == "" || password == "" {
		return "", fmt.Errorf("email/username and password are required")
	}

	var username, storedHash string
	err := s.db.QueryRow(
		"SELECT username, password FROM users WHERE LOWER(username) = LOWER(?) OR LOWER(email) = LOWER(?) LIMIT 1",
		identifier, identifier,
	).Scan(&username, &storedHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("invalid email/username or password")
		}
		return "", err
	}

	matched, needsUpgrade := verifyPassword(storedHash, password)
	if !matched {
		return "", fmt.Errorf("invalid email/username or password")
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

// UserProfile represents public/editable profile information for a user.
type UserProfile struct {
	Username  string `json:"username"`
	FullName  string `json:"full_name"`
	Email     string `json:"email"`
	Currency  string `json:"currency"`
	CreatedAt string `json:"created_at"`
}

func (s *DBStore) GetProfile(username string) (*UserProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var prof UserProfile
	var createdAt time.Time
	var email, fullName, curr sql.NullString

	err := s.db.QueryRow(
		"SELECT username, full_name, email, currency, created_at FROM users WHERE username = ?",
		username,
	).Scan(&prof.Username, &fullName, &email, &curr, &createdAt)
	if err != nil {
		return nil, err
	}
	if fullName.Valid {
		prof.FullName = fullName.String
	}
	if email.Valid {
		prof.Email = email.String
	}
	if curr.Valid && curr.String != "" {
		prof.Currency = curr.String
	} else {
		prof.Currency = "₦"
	}
	prof.CreatedAt = createdAt.Format(time.RFC3339)
	return &prof, nil
}

func (s *DBStore) UpdateProfile(username, fullName, email, currency, currentPass, newPass string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fullName = strings.TrimSpace(fullName)
	email = strings.ToLower(strings.TrimSpace(email))
	currency = strings.TrimSpace(currency)

	if email != "" {
		if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
			return fmt.Errorf("please enter a valid email address")
		}
		var emailExists int
		err := s.db.QueryRow("SELECT COUNT(1) FROM users WHERE LOWER(email) = LOWER(?) AND username != ?", email, username).Scan(&emailExists)
		if err != nil {
			return err
		}
		if emailExists > 0 {
			return fmt.Errorf("email address already in use by another account")
		}
	}

	if currency == "" {
		currency = "₦"
	}

	if newPass != "" {
		if len(newPass) < 6 {
			return fmt.Errorf("new password must be at least 6 characters")
		}
		if currentPass == "" {
			return fmt.Errorf("current password is required to set a new password")
		}

		var storedHash string
		err := s.db.QueryRow("SELECT password FROM users WHERE username = ?", username).Scan(&storedHash)
		if err != nil {
			return err
		}

		matched, _ := verifyPassword(storedHash, currentPass)
		if !matched {
			return fmt.Errorf("current password is incorrect")
		}

		newHash, err := createPasswordHash(newPass)
		if err != nil {
			return err
		}

		_, err = s.db.Exec("UPDATE users SET full_name = ?, email = ?, currency = ?, password = ? WHERE username = ?",
			fullName, email, currency, newHash, username)
		return err
	}

	_, err := s.db.Exec("UPDATE users SET full_name = ?, email = ?, currency = ? WHERE username = ?",
		fullName, email, currency, username)
	return err
}

// ─── Transaction Methods ───────────────────────────────────────────────────

func parseTags(tags string) (string, []string) {
	tags = strings.TrimSpace(tags)
	if tags == "" {
		return "", []string{}
	}
	parts := strings.FieldsFunc(tags, func(r rune) bool {
		return r == ',' || r == ';'
	})
	var cleaned []string
	seen := make(map[string]bool)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "#")
		p = strings.ToLower(p)
		if p != "" && !seen[p] {
			seen[p] = true
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, ","), cleaned
}

func (s *DBStore) AddTransactionWithTags(username string, amount float64, category, note, txnType, tags string, optDate ...time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	txDate := time.Now()
	if len(optDate) > 0 && !optDate[0].IsZero() {
		txDate = optDate[0]
	}

	cleanTags, _ := parseTags(tags)

	_, err := s.db.Exec(
		"INSERT INTO transactions (username, amount, category, note, date, type, tags) VALUES (?, ?, ?, ?, ?, ?, ?)",
		username, amount, category, note, txDate, txnType, cleanTags,
	)
	return err
}

func (s *DBStore) AddTransaction(username string, amount float64, category, note, txnType string, optDate ...time.Time) error {
	return s.AddTransactionWithTags(username, amount, category, note, txnType, "", optDate...)
}

func (s *DBStore) UpdateTransactionWithTags(id int, username string, amount float64, category, note, txnType, tags string, optDate ...time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanTags, _ := parseTags(tags)

	var res sql.Result
	var err error

	if len(optDate) > 0 && !optDate[0].IsZero() {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ?, tags = ?, date = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, cleanTags, optDate[0], id, username,
		)
	} else {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ?, tags = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, cleanTags, id, username,
		)
	}

	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) UpdateTransaction(id int, username string, amount float64, category, note, txnType string, optDate ...time.Time) (bool, error) {
	return s.UpdateTransactionWithTags(id, username, amount, category, note, txnType, "", optDate...)
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
		"SELECT id, amount, category, note, date, type, COALESCE(tags, '') FROM transactions WHERE username = ? ORDER BY date DESC, id DESC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txs []Transaction
	for rows.Next() {
		var t Transaction
		var rawTags string
		if err := rows.Scan(&t.ID, &t.Amount, &t.Category, &t.Note, &t.Date, &t.Type, &rawTags); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}
		t.Tags = rawTags
		if rawTags != "" {
			_, t.TagList = parseTags(rawTags)
		} else {
			t.TagList = []string{}
		}
		txs = append(txs, t)
	}
	if txs == nil {
		txs = []Transaction{}
	}
	return txs, nil
}

func (s *DBStore) GetPopularTags(username string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT tags FROM transactions WHERE username = ? AND tags != ''", username)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err == nil {
			_, list := parseTags(raw)
			for _, tag := range list {
				counts[tag]++
			}
		}
	}

	type tagCount struct {
		tag   string
		count int
	}
	var sorted []tagCount
	for k, v := range counts {
		sorted = append(sorted, tagCount{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	var result []string
	for _, tc := range sorted {
		result = append(result, tc.tag)
	}
	if result == nil {
		result = []string{}
	}
	return result, nil
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

func (s *DBStore) GetTrendsByTimeFrame(username, timeframe, startDateStr, endDateStr string) ([]MonthlySummary, error) {
	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}
	return CalculateTrends(txs, timeframe, startDateStr, endDateStr), nil
}

func (s *DBStore) GetMonthlyTrends(username string, monthsBack int) ([]MonthlySummary, error) {
	if monthsBack <= 0 {
		monthsBack = 6
	}
	return s.GetTrendsByTimeFrame(username, fmt.Sprintf("%dm", monthsBack), "", "")
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

// ─── Debts & IOUs Methods ───────────────────────────────────────────────────

// Debt represents money owed by or to a user.
type Debt struct {
	ID         int        `json:"id"`
	Username   string     `json:"username"`
	PersonName string     `json:"person_name"`
	Amount     float64    `json:"amount"`
	AmountPaid float64    `json:"amount_paid"`
	Remaining  float64    `json:"remaining"`
	Type       string     `json:"type"` // "i_owe" (Liability/Debt) or "owing_me" (Asset/Loan)
	DueDate    *time.Time `json:"due_date,omitempty"`
	DueDateFmt string     `json:"due_date_fmt,omitempty"`
	Note       string     `json:"note"`
	Status     string     `json:"status"` // "unpaid", "partial", "settled"
	CreatedAt  time.Time  `json:"created_at"`
}

// DebtSummary holds summary metrics for debts and loans.
type DebtSummary struct {
	TotalOwedToUser float64 `json:"total_owed_to_user"` // sum of remaining where type == "owing_me"
	TotalUserOwes   float64 `json:"total_user_owes"`   // sum of remaining where type == "i_owe"
	NetBalance      float64 `json:"net_balance"`       // total_owed_to_user - total_user_owes
	CountOwingMe    int     `json:"count_owing_me"`    // active count
	CountIOwe       int     `json:"count_i_owe"`       // active count
	TotalSettled    int     `json:"total_settled"`
}

func (s *DBStore) CreateDebt(username, personName, debtType string, amount float64, dueDate *time.Time, note string) (*Debt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	personName = strings.TrimSpace(personName)
	if personName == "" {
		return nil, fmt.Errorf("person name is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	debtType = strings.ToLower(strings.TrimSpace(debtType))
	if debtType != "i_owe" && debtType != "owing_me" {
		debtType = "owing_me"
	}

	createdAt := time.Now()
	res, err := s.db.Exec(
		"INSERT INTO debts (username, person_name, amount, amount_paid, type, due_date, note, status, created_at) VALUES (?, ?, ?, 0, ?, ?, ?, 'unpaid', ?)",
		username, personName, amount, debtType, dueDate, note, createdAt,
	)
	if err != nil {
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	debt := &Debt{
		ID:         int(id),
		Username:   username,
		PersonName: personName,
		Amount:     amount,
		AmountPaid: 0,
		Remaining:  amount,
		Type:       debtType,
		DueDate:    dueDate,
		Note:       note,
		Status:     "unpaid",
		CreatedAt:  createdAt,
	}
	if dueDate != nil && !dueDate.IsZero() {
		debt.DueDateFmt = dueDate.Format("2006-01-02")
	}
	return debt, nil
}

func (s *DBStore) GetDebts(username string) ([]Debt, *DebtSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, person_name, amount, amount_paid, type, due_date, note, status, created_at FROM debts WHERE username = ? ORDER BY CASE status WHEN 'unpaid' THEN 1 WHEN 'partial' THEN 2 ELSE 3 END, created_at DESC",
		username,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var debts []Debt
	summary := &DebtSummary{}

	for rows.Next() {
		var d Debt
		var dueDate sql.NullTime
		if err := rows.Scan(&d.ID, &d.Username, &d.PersonName, &d.Amount, &d.AmountPaid, &d.Type, &dueDate, &d.Note, &d.Status, &d.CreatedAt); err != nil {
			log.Printf("scan debt error: %v", err)
			continue
		}
		if dueDate.Valid {
			t := dueDate.Time
			d.DueDate = &t
			d.DueDateFmt = t.Format("2006-01-02")
		}

		rem := d.Amount - d.AmountPaid
		if rem < 0 {
			rem = 0
		}
		d.Remaining = rem

		if d.Status == "settled" {
			summary.TotalSettled++
		} else {
			if d.Type == "owing_me" {
				summary.TotalOwedToUser += rem
				summary.CountOwingMe++
			} else {
				summary.TotalUserOwes += rem
				summary.CountIOwe++
			}
		}

		debts = append(debts, d)
	}

	summary.NetBalance = summary.TotalOwedToUser - summary.TotalUserOwes
	return debts, summary, nil
}

func (s *DBStore) GetDebtByID(id int, username string) (*Debt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var d Debt
	var dueDate sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, person_name, amount, amount_paid, type, due_date, note, status, created_at FROM debts WHERE id = ? AND username = ?",
		id, username,
	).Scan(&d.ID, &d.Username, &d.PersonName, &d.Amount, &d.AmountPaid, &d.Type, &dueDate, &d.Note, &d.Status, &d.CreatedAt)
	if err != nil {
		return nil, err
	}

	if dueDate.Valid {
		t := dueDate.Time
		d.DueDate = &t
		d.DueDateFmt = t.Format("2006-01-02")
	}
	rem := d.Amount - d.AmountPaid
	if rem < 0 {
		rem = 0
	}
	d.Remaining = rem
	return &d, nil
}

func (s *DBStore) UpdateDebt(id int, username, personName, debtType string, amount float64, dueDate *time.Time, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	personName = strings.TrimSpace(personName)
	if personName == "" {
		return fmt.Errorf("person name is required")
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}

	// Fetch current amount_paid to adjust status
	var amountPaid float64
	err := s.db.QueryRow("SELECT amount_paid FROM debts WHERE id = ? AND username = ?", id, username).Scan(&amountPaid)
	if err != nil {
		return err
	}

	newStatus := "unpaid"
	if amountPaid >= amount {
		newStatus = "settled"
	} else if amountPaid > 0 {
		newStatus = "partial"
	}

	_, err = s.db.Exec(
		"UPDATE debts SET person_name = ?, type = ?, amount = ?, due_date = ?, note = ?, status = ? WHERE id = ? AND username = ?",
		personName, debtType, amount, dueDate, note, newStatus, id, username,
	)
	return err
}

func (s *DBStore) DeleteDebt(id int, username string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec("DELETE FROM debts WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) RecordDebtPayment(id int, username string, paymentAmount float64, paymentDate time.Time, logTransaction bool) (*Debt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if paymentAmount <= 0 {
		return nil, fmt.Errorf("payment amount must be greater than zero")
	}

	var d Debt
	var dueDate sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, person_name, amount, amount_paid, type, due_date, note, status, created_at FROM debts WHERE id = ? AND username = ?",
		id, username,
	).Scan(&d.ID, &d.Username, &d.PersonName, &d.Amount, &d.AmountPaid, &d.Type, &dueDate, &d.Note, &d.Status, &d.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("debt not found: %w", err)
	}

	if d.Status == "settled" {
		return nil, fmt.Errorf("this debt is already fully settled")
	}

	remaining := d.Amount - d.AmountPaid
	if paymentAmount > remaining {
		paymentAmount = remaining
	}

	newPaid := d.AmountPaid + paymentAmount
	newStatus := "partial"
	if newPaid >= d.Amount {
		newPaid = d.Amount
		newStatus = "settled"
	}

	_, err = s.db.Exec(
		"UPDATE debts SET amount_paid = ?, status = ? WHERE id = ? AND username = ?",
		newPaid, newStatus, id, username,
	)
	if err != nil {
		return nil, err
	}

	d.AmountPaid = newPaid
	rem := d.Amount - newPaid
	if rem < 0 {
		rem = 0
	}
	d.Remaining = rem
	d.Status = newStatus

	if dueDate.Valid {
		t := dueDate.Time
		d.DueDate = &t
		d.DueDateFmt = t.Format("2006-01-02")
	}

	// Automatically record transaction into main ledger if requested
	if logTransaction {
		if paymentDate.IsZero() {
			paymentDate = time.Now()
		}

		var txnType, category, txNote string
		if d.Type == "owing_me" {
			// Money owed to user was received -> Income
			txnType = "income"
			category = "refunds"
			if d.Note != "" {
				txNote = fmt.Sprintf("Payment from %s: %s", d.PersonName, d.Note)
			} else {
				txNote = fmt.Sprintf("Payment from %s", d.PersonName)
			}
		} else {
			// User paid money owed -> Expense
			txnType = "expense"
			category = "bills"
			if d.Note != "" {
				txNote = fmt.Sprintf("Debt paid to %s: %s", d.PersonName, d.Note)
			} else {
				txNote = fmt.Sprintf("Debt paid to %s", d.PersonName)
			}
		}

		_, err = s.db.Exec(
			"INSERT INTO transactions (username, amount, category, note, date, type) VALUES (?, ?, ?, ?, ?, ?)",
			username, paymentAmount, category, txNote, paymentDate, txnType,
		)
		if err != nil {
			log.Printf("failed to log debt repayment transaction: %v", err)
		}
	}

	return &d, nil
}

// ─── Subscriptions & Recurring Bills ──────────────────────────────────────────

type Subscription struct {
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	Name         string    `json:"name"`
	Amount       float64   `json:"amount"`
	AmountFmt    string    `json:"amount_fmt,omitempty"`
	Category     string    `json:"category"`
	BillingCycle string    `json:"billing_cycle"` // "monthly", "weekly", "yearly"
	NextDueDate  time.Time `json:"next_due_date"`
	DueDateFmt   string    `json:"due_date_fmt"`
	DaysUntil    int       `json:"days_until"`
	DueStatus    string    `json:"due_status"` // "overdue", "today", "soon", "upcoming"
	Status       string    `json:"status"`     // "active", "paused", "cancelled"
	CreatedAt    time.Time `json:"created_at"`
}

func (s *DBStore) GetSubscriptions(username string) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, name, amount, category, billing_cycle, next_due_date, status, created_at FROM subscriptions WHERE username = ? AND status != 'cancelled' ORDER BY next_due_date ASC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []Subscription
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.Username, &sub.Name, &sub.Amount, &sub.Category, &sub.BillingCycle, &sub.NextDueDate, &sub.Status, &sub.CreatedAt); err != nil {
			return nil, err
		}

		sub.DueDateFmt = sub.NextDueDate.Format("2006-01-02")
		dueDay := time.Date(sub.NextDueDate.Year(), sub.NextDueDate.Month(), sub.NextDueDate.Day(), 0, 0, 0, 0, sub.NextDueDate.Location())
		days := int(dueDay.Sub(today).Hours() / 24)
		sub.DaysUntil = days

		if days < 0 {
			sub.DueStatus = "overdue"
		} else if days == 0 {
			sub.DueStatus = "today"
		} else if days <= 3 {
			sub.DueStatus = "soon"
		} else {
			sub.DueStatus = "upcoming"
		}

		subs = append(subs, sub)
	}

	if subs == nil {
		subs = []Subscription{}
	}
	return subs, nil
}

func (s *DBStore) AddSubscription(username, name string, amount float64, category, billingCycle string, nextDueDate time.Time) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("subscription name is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "bills"
	}
	billingCycle = strings.ToLower(strings.TrimSpace(billingCycle))
	if billingCycle != "weekly" && billingCycle != "yearly" {
		billingCycle = "monthly"
	}
	if nextDueDate.IsZero() {
		nextDueDate = time.Now()
	}

	res, err := s.db.Exec(
		"INSERT INTO subscriptions (username, name, amount, category, billing_cycle, next_due_date, status, created_at) VALUES (?, ?, ?, ?, ?, ?, 'active', ?)",
		username, name, amount, category, billingCycle, nextDueDate, time.Now(),
	)
	if err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	sub := &Subscription{
		ID:           int(id),
		Username:     username,
		Name:         name,
		Amount:       amount,
		Category:     category,
		BillingCycle: billingCycle,
		NextDueDate:  nextDueDate,
		DueDateFmt:   nextDueDate.Format("2006-01-02"),
		Status:       "active",
		CreatedAt:    time.Now(),
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDay := time.Date(nextDueDate.Year(), nextDueDate.Month(), nextDueDate.Day(), 0, 0, 0, 0, nextDueDate.Location())
	sub.DaysUntil = int(dueDay.Sub(today).Hours() / 24)
	if sub.DaysUntil < 0 {
		sub.DueStatus = "overdue"
	} else if sub.DaysUntil == 0 {
		sub.DueStatus = "today"
	} else if sub.DaysUntil <= 3 {
		sub.DueStatus = "soon"
	} else {
		sub.DueStatus = "upcoming"
	}

	return sub, nil
}

func (s *DBStore) UpdateSubscription(id int, username, name string, amount float64, category, billingCycle string, nextDueDate time.Time, status string) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("subscription name is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "bills"
	}
	billingCycle = strings.ToLower(strings.TrimSpace(billingCycle))
	if billingCycle != "weekly" && billingCycle != "yearly" {
		billingCycle = "monthly"
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "paused" && status != "cancelled" {
		status = "active"
	}

	_, err := s.db.Exec(
		"UPDATE subscriptions SET name = ?, amount = ?, category = ?, billing_cycle = ?, next_due_date = ?, status = ? WHERE id = ? AND username = ?",
		name, amount, category, billingCycle, nextDueDate, status, id, username,
	)
	if err != nil {
		return nil, err
	}

	sub := &Subscription{
		ID:           id,
		Username:     username,
		Name:         name,
		Amount:       amount,
		Category:     category,
		BillingCycle: billingCycle,
		NextDueDate:  nextDueDate,
		DueDateFmt:   nextDueDate.Format("2006-01-02"),
		Status:       status,
	}
	return sub, nil
}

func (s *DBStore) DeleteSubscription(id int, username string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec("DELETE FROM subscriptions WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) PaySubscription(id int, username string) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sub Subscription
	err := s.db.QueryRow(
		"SELECT id, username, name, amount, category, billing_cycle, next_due_date, status, created_at FROM subscriptions WHERE id = ? AND username = ?",
		id, username,
	).Scan(&sub.ID, &sub.Username, &sub.Name, &sub.Amount, &sub.Category, &sub.BillingCycle, &sub.NextDueDate, &sub.Status, &sub.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("subscription not found: %w", err)
	}

	// 1. Record expense transaction
	txNote := fmt.Sprintf("%s (%s bill)", sub.Name, sub.BillingCycle)
	_, err = s.db.Exec(
		"INSERT INTO transactions (username, amount, category, note, date, type) VALUES (?, ?, ?, ?, ?, 'expense')",
		username, sub.Amount, sub.Category, txNote, time.Now(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to log payment transaction: %w", err)
	}

	// 2. Advance next due date
	next := sub.NextDueDate
	switch strings.ToLower(sub.BillingCycle) {
	case "weekly":
		next = next.AddDate(0, 0, 7)
	case "yearly":
		next = next.AddDate(1, 0, 0)
	default:
		next = next.AddDate(0, 1, 0)
	}

	now := time.Now()
	for next.Before(now) {
		switch strings.ToLower(sub.BillingCycle) {
		case "weekly":
			next = next.AddDate(0, 0, 7)
		case "yearly":
			next = next.AddDate(1, 0, 0)
		default:
			next = next.AddDate(0, 1, 0)
		}
	}

	_, err = s.db.Exec(
		"UPDATE subscriptions SET next_due_date = ? WHERE id = ? AND username = ?",
		next, id, username,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to advance subscription due date: %w", err)
	}

	sub.NextDueDate = next
	sub.DueDateFmt = next.Format("2006-01-02")
	return &sub, nil
}

func (s *DBStore) GetMonthlyCommitment(username string) (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT amount, billing_cycle FROM subscriptions WHERE username = ? AND status = 'active'",
		username,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var total float64
	for rows.Next() {
		var amount float64
		var cycle string
		if err := rows.Scan(&amount, &cycle); err != nil {
			return 0, err
		}
		switch strings.ToLower(cycle) {
		case "weekly":
			total += amount * 4.333
		case "yearly":
			total += amount / 12.0
		default: // monthly
			total += amount
		}
	}
	return total, nil
}

// ─── Savings Goals & Virtual Pots ──────────────────────────────────────────

type Goal struct {
	ID            int        `json:"id"`
	Username      string     `json:"username"`
	Name          string     `json:"name"`
	TargetAmount  float64    `json:"target_amount"`
	SavedAmount   float64    `json:"saved_amount"`
	Remaining     float64    `json:"remaining"`
	Percentage    int        `json:"percentage"`
	TargetFmt     string     `json:"target_fmt,omitempty"`
	SavedFmt      string     `json:"saved_fmt,omitempty"`
	RemainingFmt  string     `json:"remaining_fmt,omitempty"`
	Category      string     `json:"category"`
	TargetDate    *time.Time `json:"target_date,omitempty"`
	TargetDateFmt string     `json:"target_date_fmt,omitempty"`
	DaysLeft      *int       `json:"days_left,omitempty"`
	Color         string     `json:"color"`
	Emoji         string     `json:"emoji"`
	Status        string     `json:"status"` // "in_progress", "completed"
	CreatedAt     time.Time  `json:"created_at"`
}

type GoalContribution struct {
	ID        int       `json:"id"`
	GoalID    int       `json:"goal_id"`
	Username  string    `json:"username"`
	Amount    float64   `json:"amount"`
	AmountFmt string    `json:"amount_fmt,omitempty"`
	Type      string    `json:"type"` // "deposit" or "withdrawal"
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	DateFmt   string    `json:"date_fmt"`
}

type GoalSummary struct {
	TotalSaved      float64 `json:"total_saved"`
	TotalTarget     float64 `json:"total_target"`
	TotalRemaining  float64 `json:"total_remaining"`
	TotalSavedFmt   string  `json:"total_saved_fmt,omitempty"`
	TotalTargetFmt  string  `json:"total_target_fmt,omitempty"`
	TotalRemainFmt  string  `json:"total_remaining_fmt,omitempty"`
	TotalGoals      int     `json:"total_goals"`
	CompletedGoals  int     `json:"completed_goals"`
	InProgressGoals int     `json:"in_progress_goals"`
	OverallProgress int     `json:"overall_progress"`
}

func (s *DBStore) GetGoals(username string) ([]Goal, GoalSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, name, target_amount, saved_amount, category, target_date, color, emoji, status, created_at FROM goals WHERE username = ? ORDER BY status ASC, created_at DESC",
		username,
	)
	if err != nil {
		return nil, GoalSummary{}, err
	}
	defer rows.Close()

	var goals []Goal
	var summary GoalSummary
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	for rows.Next() {
		var g Goal
		var targetDate sql.NullTime
		if err := rows.Scan(&g.ID, &g.Username, &g.Name, &g.TargetAmount, &g.SavedAmount, &g.Category, &targetDate, &g.Color, &g.Emoji, &g.Status, &g.CreatedAt); err != nil {
			return nil, GoalSummary{}, err
		}

		if targetDate.Valid {
			t := targetDate.Time
			g.TargetDate = &t
			g.TargetDateFmt = t.Format("2006-01-02")
			targetDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			days := int(targetDay.Sub(today).Hours() / 24)
			g.DaysLeft = &days
		}

		rem := g.TargetAmount - g.SavedAmount
		if rem < 0 {
			rem = 0
		}
		g.Remaining = rem

		pct := 0
		if g.TargetAmount > 0 {
			pct = int((g.SavedAmount / g.TargetAmount) * 100)
			if pct > 100 {
				pct = 100
			}
		}
		g.Percentage = pct

		if g.Color == "" {
			g.Color = "#10B981"
		}
		if g.Emoji == "" {
			g.Emoji = "🎯"
		}

		summary.TotalSaved += g.SavedAmount
		summary.TotalTarget += g.TargetAmount
		summary.TotalRemaining += rem
		summary.TotalGoals++
		if g.Status == "completed" || g.SavedAmount >= g.TargetAmount {
			summary.CompletedGoals++
		} else {
			summary.InProgressGoals++
		}

		goals = append(goals, g)
	}

	if goals == nil {
		goals = []Goal{}
	}

	if summary.TotalTarget > 0 {
		summary.OverallProgress = int((summary.TotalSaved / summary.TotalTarget) * 100)
		if summary.OverallProgress > 100 {
			summary.OverallProgress = 100
		}
	}

	return goals, summary, nil
}

func (s *DBStore) GetGoalByID(id int, username string) (*Goal, []GoalContribution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var g Goal
	var targetDate sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, name, target_amount, saved_amount, category, target_date, color, emoji, status, created_at FROM goals WHERE id = ? AND username = ?",
		id, username,
	).Scan(&g.ID, &g.Username, &g.Name, &g.TargetAmount, &g.SavedAmount, &g.Category, &targetDate, &g.Color, &g.Emoji, &g.Status, &g.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("goal not found: %w", err)
	}

	if targetDate.Valid {
		t := targetDate.Time
		g.TargetDate = &t
		g.TargetDateFmt = t.Format("2006-01-02")
	}

	rem := g.TargetAmount - g.SavedAmount
	if rem < 0 {
		rem = 0
	}
	g.Remaining = rem

	pct := 0
	if g.TargetAmount > 0 {
		pct = int((g.SavedAmount / g.TargetAmount) * 100)
		if pct > 100 {
			pct = 100
		}
	}
	g.Percentage = pct

	// Fetch contributions history
	cRows, err := s.db.Query(
		"SELECT id, goal_id, username, amount, type, note, created_at FROM goal_contributions WHERE goal_id = ? ORDER BY created_at DESC LIMIT 50",
		id,
	)
	var contributions []GoalContribution
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var c GoalContribution
			if err := cRows.Scan(&c.ID, &c.GoalID, &c.Username, &c.Amount, &c.Type, &c.Note, &c.CreatedAt); err == nil {
				c.DateFmt = c.CreatedAt.Format("Jan 02, 2006 15:04")
				contributions = append(contributions, c)
			}
		}
	}
	if contributions == nil {
		contributions = []GoalContribution{}
	}

	return &g, contributions, nil
}

func (s *DBStore) CreateGoal(username, name string, targetAmount float64, targetDate *time.Time, emoji, color, category string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("goal name is required")
	}
	if targetAmount <= 0 {
		return nil, fmt.Errorf("target amount must be greater than zero")
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		emoji = "🎯"
	}
	color = strings.TrimSpace(color)
	if color == "" {
		color = "#10B981"
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "savings"
	}

	res, err := s.db.Exec(
		"INSERT INTO goals (username, name, target_amount, saved_amount, category, target_date, color, emoji, status, created_at) VALUES (?, ?, ?, 0, ?, ?, ?, ?, 'in_progress', ?)",
		username, name, targetAmount, category, targetDate, color, emoji, time.Now(),
	)
	if err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	g := &Goal{
		ID:           int(id),
		Username:     username,
		Name:         name,
		TargetAmount: targetAmount,
		SavedAmount:  0,
		Remaining:    targetAmount,
		Percentage:   0,
		Category:     category,
		TargetDate:   targetDate,
		Color:        color,
		Emoji:        emoji,
		Status:       "in_progress",
		CreatedAt:    time.Now(),
	}
	if targetDate != nil {
		g.TargetDateFmt = targetDate.Format("2006-01-02")
	}
	return g, nil
}

func (s *DBStore) UpdateGoal(id int, username, name string, targetAmount float64, targetDate *time.Time, emoji, color, category, status string) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("goal name is required")
	}
	if targetAmount <= 0 {
		return nil, fmt.Errorf("target amount must be greater than zero")
	}
	if emoji == "" {
		emoji = "🎯"
	}
	if color == "" {
		color = "#10B981"
	}
	if category == "" {
		category = "savings"
	}
	if status != "completed" {
		status = "in_progress"
	}

	_, err := s.db.Exec(
		"UPDATE goals SET name = ?, target_amount = ?, target_date = ?, color = ?, emoji = ?, category = ?, status = ? WHERE id = ? AND username = ?",
		name, targetAmount, targetDate, color, emoji, category, status, id, username,
	)
	if err != nil {
		return nil, err
	}

	g := &Goal{
		ID:           id,
		Username:     username,
		Name:         name,
		TargetAmount: targetAmount,
		Category:     category,
		TargetDate:   targetDate,
		Color:        color,
		Emoji:        emoji,
		Status:       status,
	}
	if targetDate != nil {
		g.TargetDateFmt = targetDate.Format("2006-01-02")
	}
	return g, nil
}

func (s *DBStore) DeleteGoal(id int, username string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.db.Exec("DELETE FROM goal_contributions WHERE goal_id = ? AND username = ?", id, username)
	res, err := s.db.Exec("DELETE FROM goals WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) DepositToGoal(id int, username string, amount float64, note string, logTransaction bool) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if amount <= 0 {
		return nil, fmt.Errorf("deposit amount must be greater than zero")
	}

	var g Goal
	var targetDate sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, name, target_amount, saved_amount, category, target_date, color, emoji, status, created_at FROM goals WHERE id = ? AND username = ?",
		id, username,
	).Scan(&g.ID, &g.Username, &g.Name, &g.TargetAmount, &g.SavedAmount, &g.Category, &targetDate, &g.Color, &g.Emoji, &g.Status, &g.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("goal not found: %w", err)
	}

	newSaved := g.SavedAmount + amount
	newStatus := g.Status
	if newSaved >= g.TargetAmount {
		newStatus = "completed"
	}

	_, err = s.db.Exec(
		"UPDATE goals SET saved_amount = ?, status = ? WHERE id = ? AND username = ?",
		newSaved, newStatus, id, username,
	)
	if err != nil {
		return nil, err
	}

	// Record contribution
	if note == "" {
		note = "Deposit into savings goal"
	}
	_, _ = s.db.Exec(
		"INSERT INTO goal_contributions (goal_id, username, amount, type, note, created_at) VALUES (?, ?, ?, 'deposit', ?, ?)",
		id, username, amount, note, time.Now(),
	)

	// Log transaction as an expense from main balance if requested
	if logTransaction {
		txNote := fmt.Sprintf("Deposit to Goal [%s]: %s", g.Name, note)
		_, err = s.db.Exec(
			"INSERT INTO transactions (username, amount, category, note, date, type) VALUES (?, ?, 'savings', ?, ?, 'expense')",
			username, amount, txNote, time.Now(),
		)
		if err != nil {
			log.Printf("failed to log deposit transaction: %v", err)
		}
	}

	g.SavedAmount = newSaved
	rem := g.TargetAmount - newSaved
	if rem < 0 {
		rem = 0
	}
	g.Remaining = rem
	g.Status = newStatus
	pct := int((newSaved / g.TargetAmount) * 100)
	if pct > 100 {
		pct = 100
	}
	g.Percentage = pct
	if targetDate.Valid {
		t := targetDate.Time
		g.TargetDate = &t
		g.TargetDateFmt = t.Format("2006-01-02")
	}

	return &g, nil
}

func (s *DBStore) WithdrawFromGoal(id int, username string, amount float64, note string, logTransaction bool) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if amount <= 0 {
		return nil, fmt.Errorf("withdrawal amount must be greater than zero")
	}

	var g Goal
	var targetDate sql.NullTime
	err := s.db.QueryRow(
		"SELECT id, username, name, target_amount, saved_amount, category, target_date, color, emoji, status, created_at FROM goals WHERE id = ? AND username = ?",
		id, username,
	).Scan(&g.ID, &g.Username, &g.Name, &g.TargetAmount, &g.SavedAmount, &g.Category, &targetDate, &g.Color, &g.Emoji, &g.Status, &g.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("goal not found: %w", err)
	}

	if amount > g.SavedAmount {
		return nil, fmt.Errorf("cannot withdraw more than current saved amount (%.2f)", g.SavedAmount)
	}

	newSaved := g.SavedAmount - amount
	newStatus := g.Status
	if newSaved < g.TargetAmount {
		newStatus = "in_progress"
	}

	_, err = s.db.Exec(
		"UPDATE goals SET saved_amount = ?, status = ? WHERE id = ? AND username = ?",
		newSaved, newStatus, id, username,
	)
	if err != nil {
		return nil, err
	}

	// Record contribution
	if note == "" {
		note = "Withdrawal from savings goal"
	}
	_, _ = s.db.Exec(
		"INSERT INTO goal_contributions (goal_id, username, amount, type, note, created_at) VALUES (?, ?, ?, 'withdrawal', ?, ?)",
		id, username, amount, note, time.Now(),
	)

	// Log transaction as an income back into main balance if requested
	if logTransaction {
		txNote := fmt.Sprintf("Withdrawal from Goal [%s]: %s", g.Name, note)
		_, err = s.db.Exec(
			"INSERT INTO transactions (username, amount, category, note, date, type) VALUES (?, ?, 'savings', ?, ?, 'income')",
			username, amount, txNote, time.Now(),
		)
		if err != nil {
			log.Printf("failed to log withdrawal transaction: %v", err)
		}
	}

	g.SavedAmount = newSaved
	rem := g.TargetAmount - newSaved
	if rem < 0 {
		rem = 0
	}
	g.Remaining = rem
	g.Status = newStatus
	pct := int((newSaved / g.TargetAmount) * 100)
	if pct > 100 {
		pct = 100
	}
	g.Percentage = pct
	if targetDate.Valid {
		t := targetDate.Time
		g.TargetDate = &t
		g.TargetDateFmt = t.Format("2006-01-02")
	}

	return &g, nil
}

// ─── Custom Categories & Tags ───────────────────────────────────────────────

type Category struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	Label     string    `json:"label"`
	Slug      string    `json:"slug"`
	Key       string    `json:"key"`
	Type      string    `json:"type"` // "expense" or "income"
	Color     string    `json:"color"`
	Emoji     string    `json:"emoji"`
	Icon      string    `json:"icon"`
	IsDefault bool      `json:"is_default"`
	TxCount   int       `json:"tx_count"`
	CreatedAt time.Time `json:"created_at"`
}

var defaultCategoryPresets = []struct {
	Name  string
	Slug  string
	Type  string
	Color string
	Emoji string
}{
	// Expense
	{"Food & Dining", "food", "expense", "#F59E0B", "🍔"},
	{"Transportation", "transport", "expense", "#3B82F6", "🚗"},
	{"Housing & Utilities", "housing", "expense", "#8B5CF6", "🏠"},
	{"Entertainment", "entertainment", "expense", "#EC4899", "🎬"},
	{"Shopping & Retail", "shopping", "expense", "#10B981", "🛍️"},
	{"Healthcare & Medical", "healthcare", "expense", "#EF4444", "💊"},
	{"Education & Courses", "education", "expense", "#6366F1", "📚"},
	{"Bills & Subscriptions", "bills", "expense", "#06B6D4", "💡"},
	{"Personal Care", "personal", "expense", "#F43F5E", "✨"},
	{"Savings & Vaults", "savings", "expense", "#14B8A6", "💰"},
	{"General Expense", "other", "expense", "#64748B", "📦"},

	// Income
	{"Salary & Wages", "salary", "income", "#10B981", "💼"},
	{"Freelance & Side Hustle", "freelance", "income", "#3B82F6", "💻"},
	{"Investments & Dividends", "investments", "income", "#8B5CF6", "📈"},
	{"Business & Sales", "business", "income", "#F59E0B", "🏢"},
	{"Rental Income", "rental", "income", "#06B6D4", "🏠"},
	{"Gifts & Grants", "gifts", "income", "#EC4899", "🎁"},
	{"Refunds & Cashback", "refunds", "income", "#14B8A6", "🔄"},
	{"Other Income", "other_income", "income", "#64748B", "💵"},
}

func (s *DBStore) EnsureDefaultCategories(username string) error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(1) FROM categories WHERE username = ?", username).Scan(&count)
	if err == nil && count > 0 {
		return nil
	}

	for _, d := range defaultCategoryPresets {
		_, _ = s.db.Exec(
			"INSERT INTO categories (username, name, slug, type, color, emoji, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?)",
			username, d.Name, d.Slug, d.Type, d.Color, d.Emoji, time.Now(),
		)
	}
	return nil
}

func (s *DBStore) GetCategories(username string) ([]Category, error) {
	s.mu.Lock()
	_ = s.EnsureDefaultCategories(username)
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	txCounts := make(map[string]int)
	countRows, err := s.db.Query("SELECT category, COUNT(1) FROM transactions WHERE username = ? GROUP BY category", username)
	if err == nil {
		defer countRows.Close()
		for countRows.Next() {
			var cat string
			var c int
			if err := countRows.Scan(&cat, &c); err == nil {
				txCounts[strings.ToLower(cat)] = c
			}
		}
	}

	rows, err := s.db.Query(
		"SELECT id, username, name, slug, type, color, emoji, is_default, created_at FROM categories WHERE username = ? ORDER BY type DESC, is_default DESC, name ASC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var cat Category
		if err := rows.Scan(&cat.ID, &cat.Username, &cat.Name, &cat.Slug, &cat.Type, &cat.Color, &cat.Emoji, &cat.IsDefault, &cat.CreatedAt); err != nil {
			continue
		}
		cat.Label = cat.Name
		cat.Key = cat.Slug
		cat.Icon = cat.Emoji
		cat.TxCount = txCounts[strings.ToLower(cat.Slug)]
		if cat.TxCount == 0 {
			cat.TxCount = txCounts[strings.ToLower(cat.Name)]
		}
		categories = append(categories, cat)
	}
	if categories == nil {
		categories = []Category{}
	}
	return categories, nil
}

func (s *DBStore) CreateCategory(username, name, catType, emoji, color string) (*Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("category name is required")
	}
	catType = strings.ToLower(strings.TrimSpace(catType))
	if catType != "income" {
		catType = "expense"
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		emoji = "🏷️"
	}
	color = strings.TrimSpace(color)
	if color == "" {
		color = "#10B981"
	}

	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "_")
	slug = strings.ReplaceAll(slug, "&", "and")

	var count int
	_ = s.db.QueryRow("SELECT COUNT(1) FROM categories WHERE username = ? AND (LOWER(name) = LOWER(?) OR slug = ?)", username, name, slug).Scan(&count)
	if count > 0 {
		return nil, fmt.Errorf("a category with this name already exists")
	}

	now := time.Now()
	res, err := s.db.Exec(
		"INSERT INTO categories (username, name, slug, type, color, emoji, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?)",
		username, name, slug, catType, color, emoji, now,
	)
	if err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	return &Category{
		ID:        int(id),
		Username:  username,
		Name:      name,
		Label:     name,
		Slug:      slug,
		Key:       slug,
		Type:      catType,
		Color:     color,
		Emoji:     emoji,
		Icon:      emoji,
		IsDefault: false,
		TxCount:   0,
		CreatedAt: now,
	}, nil
}

func (s *DBStore) UpdateCategory(id int, username, name, emoji, color string) (*Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("category name is required")
	}
	if emoji == "" {
		emoji = "🏷️"
	}
	if color == "" {
		color = "#10B981"
	}

	var cat Category
	err := s.db.QueryRow("SELECT id, username, name, slug, type, is_default, created_at FROM categories WHERE id = ? AND username = ?", id, username).
		Scan(&cat.ID, &cat.Username, &cat.Name, &cat.Slug, &cat.Type, &cat.IsDefault, &cat.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("category not found")
	}

	_, err = s.db.Exec("UPDATE categories SET name = ?, emoji = ?, color = ? WHERE id = ? AND username = ?", name, emoji, color, id, username)
	if err != nil {
		return nil, err
	}

	cat.Name = name
	cat.Label = name
	cat.Key = cat.Slug
	cat.Emoji = emoji
	cat.Icon = emoji
	cat.Color = color
	return &cat, nil
}

func (s *DBStore) DeleteCategory(id int, username string, reassignTo ...string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var cat Category
	err := s.db.QueryRow("SELECT id, slug, is_default FROM categories WHERE id = ? AND username = ?", id, username).Scan(&cat.ID, &cat.Slug, &cat.IsDefault)
	if err != nil {
		return false, fmt.Errorf("category not found")
	}

	newCat := "other"
	if len(reassignTo) > 0 && strings.TrimSpace(reassignTo[0]) != "" {
		newCat = strings.TrimSpace(reassignTo[0])
	}

	_, _ = s.db.Exec("UPDATE transactions SET category = ? WHERE username = ? AND (category = ? OR category = ?)", newCat, username, cat.Slug, cat.Name)

	res, err := s.db.Exec("DELETE FROM categories WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

