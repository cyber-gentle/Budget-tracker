package app

import (
	"database/sql"
	"fmt"
	"log"
	"math"
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

	CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		currency TEXT DEFAULT '₦',
		initial_balance REAL NOT NULL DEFAULT 0,
		color TEXT DEFAULT '#3B82F6',
		icon TEXT DEFAULT '🏦',
		is_default BOOLEAN DEFAULT 0,
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_accounts_user ON accounts(username);

	CREATE TABLE IF NOT EXISTS account_transfers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		from_account_id INTEGER NOT NULL,
		to_account_id INTEGER NOT NULL,
		amount REAL NOT NULL,
		note TEXT,
		date DATETIME,
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_transfers_user ON account_transfers(username);

	CREATE TABLE IF NOT EXISTS splits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		title TEXT NOT NULL,
		total_amount REAL NOT NULL,
		payer_name TEXT NOT NULL,
		payer_is_user INTEGER NOT NULL DEFAULT 1,
		category TEXT NOT NULL DEFAULT 'General',
		date DATETIME NOT NULL,
		split_type TEXT NOT NULL DEFAULT 'equal',
		note TEXT,
		created_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_splits_user ON splits(username);

	CREATE TABLE IF NOT EXISTS split_participants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		split_id INTEGER NOT NULL,
		username TEXT NOT NULL,
		name TEXT NOT NULL,
		share_amount REAL NOT NULL,
		is_user INTEGER NOT NULL DEFAULT 0,
		debt_id INTEGER DEFAULT NULL,
		status TEXT NOT NULL DEFAULT 'pending'
	);
	CREATE INDEX IF NOT EXISTS idx_split_participants ON split_participants(split_id);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Migrate existing tables if columns don't exist
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN email TEXT")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN full_name TEXT DEFAULT ''")
	_, _ = s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email)")
	_, _ = s.db.Exec("ALTER TABLE transactions ADD COLUMN tags TEXT DEFAULT ''")
	_, _ = s.db.Exec("ALTER TABLE transactions ADD COLUMN account_id INTEGER DEFAULT 0")
	_, _ = s.db.Exec("ALTER TABLE debts ADD COLUMN split_id INTEGER DEFAULT NULL")

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

func (s *DBStore) AddTransactionFull(username string, amount float64, category, note, txnType, tags string, accountID int, optDate ...time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	txDate := time.Now()
	if len(optDate) > 0 && !optDate[0].IsZero() {
		txDate = optDate[0]
	}

	cleanTags, _ := parseTags(tags)

	_, err := s.db.Exec(
		"INSERT INTO transactions (username, amount, category, note, date, type, tags, account_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		username, amount, category, note, txDate, txnType, cleanTags, accountID,
	)
	return err
}

func (s *DBStore) AddTransactionWithTags(username string, amount float64, category, note, txnType, tags string, optDate ...time.Time) error {
	return s.AddTransactionFull(username, amount, category, note, txnType, tags, 0, optDate...)
}

func (s *DBStore) AddTransaction(username string, amount float64, category, note, txnType string, optDate ...time.Time) error {
	return s.AddTransactionFull(username, amount, category, note, txnType, "", 0, optDate...)
}

func (s *DBStore) UpdateTransactionFull(id int, username string, amount float64, category, note, txnType, tags string, accountID int, optDate ...time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanTags, _ := parseTags(tags)

	var res sql.Result
	var err error

	if len(optDate) > 0 && !optDate[0].IsZero() {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ?, tags = ?, account_id = ?, date = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, cleanTags, accountID, optDate[0], id, username,
		)
	} else {
		res, err = s.db.Exec(
			"UPDATE transactions SET amount = ?, category = ?, note = ?, type = ?, tags = ?, account_id = ? WHERE id = ? AND username = ?",
			amount, category, note, txnType, cleanTags, accountID, id, username,
		)
	}

	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) UpdateTransactionWithTags(id int, username string, amount float64, category, note, txnType, tags string, optDate ...time.Time) (bool, error) {
	return s.UpdateTransactionFull(id, username, amount, category, note, txnType, tags, 0, optDate...)
}

func (s *DBStore) UpdateTransaction(id int, username string, amount float64, category, note, txnType string, optDate ...time.Time) (bool, error) {
	return s.UpdateTransactionFull(id, username, amount, category, note, txnType, "", 0, optDate...)
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
		`SELECT t.id, t.amount, t.category, t.note, t.date, t.type, COALESCE(t.tags, ''),
                COALESCE(t.account_id, 0), COALESCE(a.name, ''), COALESCE(a.icon, '')
         FROM transactions t
         LEFT JOIN accounts a ON a.id = t.account_id AND a.username = t.username
         WHERE t.username = ?
         ORDER BY t.date DESC, t.id DESC`,
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
		if err := rows.Scan(&t.ID, &t.Amount, &t.Category, &t.Note, &t.Date, &t.Type, &rawTags, &t.AccountID, &t.AccountName, &t.AccountIcon); err != nil {
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

// ─── Financial Reports & Advanced Analytics ─────────────────────────────────

type ReportKPIs struct {
	TotalIncome           float64 `json:"total_income"`
	TotalExpense          float64 `json:"total_expense"`
	NetSavings            float64 `json:"net_savings"`
	SavingsRate           int     `json:"savings_rate"`
	DailyAvgSpend         float64 `json:"daily_avg_spend"`
	DailyAvgIncome        float64 `json:"daily_avg_income"`
	TransactionCount      int     `json:"transaction_count"`
	IncomeCount           int     `json:"income_count"`
	ExpenseCount          int     `json:"expense_count"`
	LargestExpenseAmount  float64 `json:"largest_expense_amount"`
	LargestExpenseNote    string  `json:"largest_expense_note"`
	LargestExpenseDate    string  `json:"largest_expense_date"`
	TopCategoryName       string  `json:"top_category_name"`
	TopCategoryIcon       string  `json:"top_category_icon"`
	TopCategoryAmount     float64 `json:"top_category_amount"`
	EmergencyRunwayMonths float64 `json:"emergency_runway_months"`
}

type ReportTimelinePoint struct {
	PeriodKey     string  `json:"period_key"`
	Label         string  `json:"label"`
	Income        float64 `json:"income"`
	Expense       float64 `json:"expense"`
	Net           float64 `json:"net"`
	CumulativeNet float64 `json:"cumulative_net"`
}

type ReportCategoryItem struct {
	Category   string  `json:"category"`
	Name       string  `json:"name"`
	Icon       string  `json:"icon"`
	Color      string  `json:"color"`
	Amount     float64 `json:"amount"`
	Percentage int     `json:"percentage"`
	Count      int     `json:"count"`
	Type       string  `json:"type"`
}

type ReportTagItem struct {
	Tag        string  `json:"tag"`
	Amount     float64 `json:"amount"`
	Count      int     `json:"count"`
	Percentage int     `json:"percentage"`
}

type ReportAccountItem struct {
	AccountID   int     `json:"account_id"`
	AccountName string  `json:"account_name"`
	Icon        string  `json:"icon"`
	Color       string  `json:"color"`
	Income      float64 `json:"income"`
	Expense     float64 `json:"expense"`
	Net         float64 `json:"net"`
	Percentage  int     `json:"percentage"`
}

type FinancialReport struct {
	Timeframe           string                `json:"timeframe"`
	TimeframeLabel      string                `json:"timeframe_label"`
	StartDate           string                `json:"start_date"`
	EndDate             string                `json:"end_date"`
	DaysInPeriod        int                   `json:"days_in_period"`
	AccountID           int                   `json:"account_id"`
	CategoryFilter      string                `json:"category_filter"`
	Currency            string                `json:"currency"`
	KPIs                ReportKPIs            `json:"kpis"`
	Timeline            []ReportTimelinePoint `json:"timeline"`
	ExpenseCategories   []ReportCategoryItem  `json:"expense_categories"`
	IncomeCategories    []ReportCategoryItem  `json:"income_categories"`
	TagsAnalytics       []ReportTagItem       `json:"tags_analytics"`
	AccountDistribution []ReportAccountItem   `json:"account_distribution"`
	Transactions        []Transaction         `json:"transactions"`
}

func (s *DBStore) GetDetailedFinancialReport(username, timeframe, startDateStr, endDateStr string, accountID int, categoryFilter string) (*FinancialReport, error) {
	now := time.Now()
	var start, end time.Time
	timeframeLabel := "This Month"

	timeframe = strings.ToLower(strings.TrimSpace(timeframe))
	if timeframe == "" {
		timeframe = "this_month"
	}

	switch timeframe {
	case "this_month":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end = start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		timeframeLabel = now.Format("January 2006")

	case "last_month":
		firstCurrent := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		start = firstCurrent.AddDate(0, -1, 0)
		end = firstCurrent.Add(-time.Nanosecond)
		timeframeLabel = start.Format("January 2006")

	case "3m", "quarter":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -2, 0)
		end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, 0).Add(-time.Nanosecond)
		timeframeLabel = "Last 3 Months"

	case "6m":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -5, 0)
		end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, 0).Add(-time.Nanosecond)
		timeframeLabel = "Last 6 Months"

	case "ytd":
		start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		end = time.Date(now.Year(), 12, 31, 23, 59, 59, 999999999, now.Location())
		timeframeLabel = fmt.Sprintf("YTD %d", now.Year())

	case "1y", "12m":
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(-1, 1, 0)
		end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, 0).Add(-time.Nanosecond)
		timeframeLabel = "Last 12 Months"

	case "all":
		start = time.Date(2000, 1, 1, 0, 0, 0, 0, now.Location())
		end = time.Date(2100, 1, 1, 0, 0, 0, 0, now.Location())
		timeframeLabel = "All Time"

	case "custom":
		timeframeLabel = "Custom Range"
		if startDateStr != "" {
			if t, err := time.Parse("2006-01-02", startDateStr); err == nil {
				start = t
			}
		}
		if endDateStr != "" {
			if t, err := time.Parse("2006-01-02", endDateStr); err == nil {
				end = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, now.Location())
			}
		}
		if start.IsZero() {
			start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		}
		if end.IsZero() {
			end = time.Now()
		}
		if start.After(end) {
			start, end = end, start
		}

	default:
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		end = start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		timeframeLabel = now.Format("January 2006")
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		days = 1
	}

	// Lookup accounts for mapping and liquid balance
	accounts, _ := s.GetAccounts(username)
	var defaultAccID int
	var totalLiquidBalance float64
	for _, a := range accounts {
		totalLiquidBalance += a.CurrentBalance
		if a.IsDefault {
			defaultAccID = a.ID
		}
	}
	if defaultAccID == 0 && len(accounts) > 0 {
		defaultAccID = accounts[0].ID
	}

	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}

	var filtered []Transaction
	categoryFilter = strings.ToLower(strings.TrimSpace(categoryFilter))

	for _, t := range txs {
		if t.Date.Before(start) || t.Date.After(end) {
			continue
		}
		effAccID := t.AccountID
		if effAccID == 0 {
			effAccID = defaultAccID
		}
		if accountID > 0 && effAccID != accountID {
			continue
		}
		if categoryFilter != "" && categoryFilter != "all" && strings.ToLower(t.Category) != categoryFilter {
			continue
		}
		filtered = append(filtered, t)
	}

	var totalIncome, totalExpense float64
	var incomeCount, expenseCount int
	var largestExpenseAmount float64
	var largestExpenseNote, largestExpenseDate string

	expCatMap := make(map[string]float64)
	expCatCount := make(map[string]int)
	incCatMap := make(map[string]float64)
	incCatCount := make(map[string]int)
	tagMap := make(map[string]float64)
	tagCount := make(map[string]int)
	accIncomeMap := make(map[int]float64)
	accExpenseMap := make(map[int]float64)

	for _, t := range filtered {
		effAccID := t.AccountID
		if effAccID == 0 {
			effAccID = defaultAccID
		}

		if t.Type == "income" {
			totalIncome += t.Amount
			incomeCount++
			incCatMap[t.Category] += t.Amount
			incCatCount[t.Category]++
			accIncomeMap[effAccID] += t.Amount
		} else {
			totalExpense += t.Amount
			expenseCount++
			expCatMap[t.Category] += t.Amount
			expCatCount[t.Category]++
			accExpenseMap[effAccID] += t.Amount

			if t.Amount > largestExpenseAmount {
				largestExpenseAmount = t.Amount
				largestExpenseNote = t.Note
				if largestExpenseNote == "" {
					largestExpenseNote = t.Category
				}
				largestExpenseDate = t.Date.Format("Jan 02, 2006")
			}

			for _, tg := range t.TagList {
				cleanTag := strings.TrimPrefix(strings.TrimSpace(tg), "#")
				if cleanTag != "" {
					tagMap[cleanTag] += t.Amount
					tagCount[cleanTag]++
				}
			}
		}
	}

	netSavings := totalIncome - totalExpense
	savingsRate := 0
	if totalIncome > 0 {
		rate := int(((totalIncome - totalExpense) / totalIncome) * 100)
		if rate > 0 {
			savingsRate = rate
		}
	}
	dailyAvgSpend := totalExpense / float64(days)
	dailyAvgIncome := totalIncome / float64(days)

	// Runway calculation
	var emergencyRunway float64
	monthlyBurn := dailyAvgSpend * 30.0
	if monthlyBurn > 0 && totalLiquidBalance > 0 {
		runway := totalLiquidBalance / monthlyBurn
		emergencyRunway = math.Round(runway*10) / 10
	}

	// Lookup categories metadata
	categories, _ := s.GetCategories(username)
	categoryMeta := make(map[string]Category)
	for _, c := range categories {
		k := c.Slug
		if k == "" {
			k = c.Key
		}
		categoryMeta[k] = c
		categoryMeta[c.Name] = c
	}

	// Expense Categories List
	var expenseCategories []ReportCategoryItem
	var topCatName, topCatIcon string
	var topCatAmount float64

	for catKey, amt := range expCatMap {
		name := catKey
		icon := "📦"
		color := "#64748B"
		if meta, ok := categoryMeta[catKey]; ok {
			name = meta.Name
			icon = meta.Icon
			if icon == "" {
				icon = meta.Emoji
			}
			color = meta.Color
		}
		pct := 0
		if totalExpense > 0 {
			pct = int(math.Round((amt / totalExpense) * 100))
		}
		expenseCategories = append(expenseCategories, ReportCategoryItem{
			Category:   catKey,
			Name:       name,
			Icon:       icon,
			Color:      color,
			Amount:     amt,
			Percentage: pct,
			Count:      expCatCount[catKey],
			Type:       "expense",
		})
	}
	sort.Slice(expenseCategories, func(i, j int) bool {
		return expenseCategories[i].Amount > expenseCategories[j].Amount
	})
	if len(expenseCategories) > 0 {
		topCatName = expenseCategories[0].Name
		topCatIcon = expenseCategories[0].Icon
		topCatAmount = expenseCategories[0].Amount
	}

	// Income Categories List
	var incomeCategories []ReportCategoryItem
	for catKey, amt := range incCatMap {
		name := catKey
		icon := "💵"
		color := "#10B981"
		if meta, ok := categoryMeta[catKey]; ok {
			name = meta.Name
			icon = meta.Icon
			if icon == "" {
				icon = meta.Emoji
			}
			color = meta.Color
		}
		pct := 0
		if totalIncome > 0 {
			pct = int(math.Round((amt / totalIncome) * 100))
		}
		incomeCategories = append(incomeCategories, ReportCategoryItem{
			Category:   catKey,
			Name:       name,
			Icon:       icon,
			Color:      color,
			Amount:     amt,
			Percentage: pct,
			Count:      incCatCount[catKey],
			Type:       "income",
		})
	}
	sort.Slice(incomeCategories, func(i, j int) bool {
		return incomeCategories[i].Amount > incomeCategories[j].Amount
	})

	// Tag Analytics
	var tagsAnalytics []ReportTagItem
	for tg, amt := range tagMap {
		pct := 0
		if totalExpense > 0 {
			pct = int(math.Round((amt / totalExpense) * 100))
		}
		tagsAnalytics = append(tagsAnalytics, ReportTagItem{
			Tag:        tg,
			Amount:     amt,
			Count:      tagCount[tg],
			Percentage: pct,
		})
	}
	sort.Slice(tagsAnalytics, func(i, j int) bool {
		return tagsAnalytics[i].Amount > tagsAnalytics[j].Amount
	})
	if len(tagsAnalytics) > 12 {
		tagsAnalytics = tagsAnalytics[:12]
	}

	// Account Distribution
	var accountDistribution []ReportAccountItem
	for _, a := range accounts {
		exp := accExpenseMap[a.ID]
		inc := accIncomeMap[a.ID]
		if exp == 0 && inc == 0 {
			continue
		}
		pct := 0
		if totalExpense > 0 {
			pct = int(math.Round((exp / totalExpense) * 100))
		}
		accountDistribution = append(accountDistribution, ReportAccountItem{
			AccountID:   a.ID,
			AccountName: a.Name,
			Icon:        a.Icon,
			Color:       a.Color,
			Income:      inc,
			Expense:     exp,
			Net:         inc - exp,
			Percentage:  pct,
		})
	}
	sort.Slice(accountDistribution, func(i, j int) bool {
		return accountDistribution[i].Expense > accountDistribution[j].Expense
	})

	// Timeline construction
	var timeline []ReportTimelinePoint
	var cumNet float64

	if days <= 35 && timeframe != "all" {
		// Daily Timeline
		dayTotals := make(map[string]*ReportTimelinePoint)
		var orderedKeys []string

		cur := start
		for !cur.After(end) {
			k := cur.Format("2006-01-02")
			lbl := cur.Format("Jan 02")
			dayTotals[k] = &ReportTimelinePoint{
				PeriodKey: k,
				Label:     lbl,
			}
			orderedKeys = append(orderedKeys, k)
			cur = cur.AddDate(0, 0, 1)
		}

		for _, t := range filtered {
			k := t.Date.Format("2006-01-02")
			if pt, ok := dayTotals[k]; ok {
				if t.Type == "income" {
					pt.Income += t.Amount
				} else {
					pt.Expense += t.Amount
				}
			}
		}

		for _, k := range orderedKeys {
			pt := dayTotals[k]
			pt.Net = pt.Income - pt.Expense
			cumNet += pt.Net
			pt.CumulativeNet = cumNet
			timeline = append(timeline, *pt)
		}
	} else {
		// Monthly Timeline
		monthTotals := make(map[string]*ReportTimelinePoint)
		var orderedKeys []string

		cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		lastMonth := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, end.Location())

		for !cur.After(lastMonth) {
			k := cur.Format("2006-01")
			lbl := cur.Format("Jan 2006")
			monthTotals[k] = &ReportTimelinePoint{
				PeriodKey: k,
				Label:     lbl,
			}
			orderedKeys = append(orderedKeys, k)
			cur = cur.AddDate(0, 1, 0)
		}

		for _, t := range filtered {
			k := t.Date.Format("2006-01")
			if pt, ok := monthTotals[k]; ok {
				if t.Type == "income" {
					pt.Income += t.Amount
				} else {
					pt.Expense += t.Amount
				}
			}
		}

		for _, k := range orderedKeys {
			pt := monthTotals[k]
			pt.Net = pt.Income - pt.Expense
			cumNet += pt.Net
			pt.CumulativeNet = cumNet
			timeline = append(timeline, *pt)
		}
	}

	// Sort filtered transactions Date DESC
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date.After(filtered[j].Date)
	})

	return &FinancialReport{
		Timeframe:      timeframe,
		TimeframeLabel: timeframeLabel,
		StartDate:      start.Format("2006-01-02"),
		EndDate:        end.Format("2006-01-02"),
		DaysInPeriod:   days,
		AccountID:      accountID,
		CategoryFilter: categoryFilter,
		Currency:       s.GetCurrency(username),
		KPIs: ReportKPIs{
			TotalIncome:           totalIncome,
			TotalExpense:          totalExpense,
			NetSavings:            netSavings,
			SavingsRate:           savingsRate,
			DailyAvgSpend:         dailyAvgSpend,
			DailyAvgIncome:        dailyAvgIncome,
			TransactionCount:      len(filtered),
			IncomeCount:           incomeCount,
			ExpenseCount:          expenseCount,
			LargestExpenseAmount:  largestExpenseAmount,
			LargestExpenseNote:    largestExpenseNote,
			LargestExpenseDate:    largestExpenseDate,
			TopCategoryName:       topCatName,
			TopCategoryIcon:       topCatIcon,
			TopCategoryAmount:     topCatAmount,
			EmergencyRunwayMonths: emergencyRunway,
		},
		Timeline:            timeline,
		ExpenseCategories:   expenseCategories,
		IncomeCategories:    incomeCategories,
		TagsAnalytics:       tagsAnalytics,
		AccountDistribution: accountDistribution,
		Transactions:        filtered,
	}, nil
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

// ─── Multi-Account & Wallet Methods ──────────────────────────────────────────

type Account struct {
	ID             int       `json:"id"`
	Username       string    `json:"username"`
	Name           string    `json:"name"`
	Type           string    `json:"type"` // bank, mobile_money, cash, savings, credit, other
	Currency       string    `json:"currency"`
	InitialBalance float64   `json:"initial_balance"`
	CurrentBalance float64   `json:"current_balance"`
	Color          string    `json:"color"`
	Icon           string    `json:"icon"`
	IsDefault      bool      `json:"is_default"`
	TxCount        int       `json:"tx_count"`
	CreatedAt      time.Time `json:"created_at"`
}

type AccountTransfer struct {
	ID              int       `json:"id"`
	Username        string    `json:"username"`
	FromAccountID   int       `json:"from_account_id"`
	FromAccountName string    `json:"from_account_name,omitempty"`
	FromAccountIcon string    `json:"from_account_icon,omitempty"`
	ToAccountID     int       `json:"to_account_id"`
	ToAccountName   string    `json:"to_account_name,omitempty"`
	ToAccountIcon   string    `json:"to_account_icon,omitempty"`
	Amount          float64   `json:"amount"`
	Note            string    `json:"note"`
	Date            time.Time `json:"date"`
	CreatedAt       time.Time `json:"created_at"`
}

func (s *DBStore) ensureDefaultAccounts(username string) error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(1) FROM accounts WHERE username = ?", username).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	curr := "₦"
	var userCurr sql.NullString
	_ = s.db.QueryRow("SELECT currency FROM users WHERE username = ?", username).Scan(&userCurr)
	if userCurr.Valid && userCurr.String != "" {
		curr = userCurr.String
	}

	now := time.Now()
	defaults := []struct {
		name      string
		accType   string
		color     string
		icon      string
		isDefault bool
	}{
		{"Main Bank", "bank", "#3B82F6", "🏦", true},
		{"Mobile Money", "mobile_money", "#10B981", "📱", false},
		{"Cash / Wallet", "cash", "#F59E0B", "💵", false},
	}

	for _, d := range defaults {
		isDefVal := 0
		if d.isDefault {
			isDefVal = 1
		}
		_, _ = s.db.Exec(
			"INSERT INTO accounts (username, name, type, currency, initial_balance, color, icon, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			username, d.name, d.accType, curr, 0.0, d.color, d.icon, isDefVal, now,
		)
	}
	return nil
}

func (s *DBStore) GetAccounts(username string) ([]Account, error) {
	s.mu.Lock()
	_ = s.ensureDefaultAccounts(username)
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, name, type, currency, initial_balance, color, icon, is_default, created_at FROM accounts WHERE username = ? ORDER BY is_default DESC, id ASC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []Account
	var defaultAccID int
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Username, &a.Name, &a.Type, &a.Currency, &a.InitialBalance, &a.Color, &a.Icon, &a.IsDefault, &a.CreatedAt); err != nil {
			continue
		}
		if a.IsDefault && defaultAccID == 0 {
			defaultAccID = a.ID
		}
		a.CurrentBalance = a.InitialBalance
		accounts = append(accounts, a)
	}

	if len(accounts) == 0 {
		return []Account{}, nil
	}
	if defaultAccID == 0 {
		defaultAccID = accounts[0].ID
	}

	// Sum income and expenses per account from transactions
	txRows, err := s.db.Query(
		"SELECT COALESCE(account_id, 0), type, SUM(amount), COUNT(1) FROM transactions WHERE username = ? GROUP BY COALESCE(account_id, 0), type",
		username,
	)
	incomeByAcc := make(map[int]float64)
	expenseByAcc := make(map[int]float64)
	txCountByAcc := make(map[int]int)

	if err == nil {
		defer txRows.Close()
		for txRows.Next() {
			var accID int
			var txnType string
			var sumAmt float64
			var cnt int
			if err := txRows.Scan(&accID, &txnType, &sumAmt, &cnt); err == nil {
				if accID == 0 {
					accID = defaultAccID
				}
				txCountByAcc[accID] += cnt
				if txnType == "income" {
					incomeByAcc[accID] += sumAmt
				} else if txnType == "expense" {
					expenseByAcc[accID] += sumAmt
				}
			}
		}
	}

	// Transfers Out
	outRows, err := s.db.Query(
		"SELECT from_account_id, SUM(amount) FROM account_transfers WHERE username = ? GROUP BY from_account_id",
		username,
	)
	transfersOut := make(map[int]float64)
	if err == nil {
		defer outRows.Close()
		for outRows.Next() {
			var fID int
			var amt float64
			if err := outRows.Scan(&fID, &amt); err == nil {
				transfersOut[fID] += amt
			}
		}
	}

	// Transfers In
	inRows, err := s.db.Query(
		"SELECT to_account_id, SUM(amount) FROM account_transfers WHERE username = ? GROUP BY to_account_id",
		username,
	)
	transfersIn := make(map[int]float64)
	if err == nil {
		defer inRows.Close()
		for inRows.Next() {
			var tID int
			var amt float64
			if err := inRows.Scan(&tID, &amt); err == nil {
				transfersIn[tID] += amt
			}
		}
	}

	for i := range accounts {
		id := accounts[i].ID
		inc := incomeByAcc[id]
		exp := expenseByAcc[id]
		tIn := transfersIn[id]
		tOut := transfersOut[id]
		accounts[i].CurrentBalance = accounts[i].InitialBalance + inc - exp + tIn - tOut
		accounts[i].TxCount = txCountByAcc[id]
	}

	return accounts, nil
}

func (s *DBStore) GetAccountByID(id int, username string) (*Account, error) {
	accounts, err := s.GetAccounts(username)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("account not found")
}

func (s *DBStore) CreateAccount(username, name, accType, currency, color, icon string, initialBalance float64, isDefault bool) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("account name is required")
	}
	accType = strings.TrimSpace(accType)
	if accType == "" {
		accType = "bank"
	}
	if color == "" {
		color = "#3B82F6"
	}
	if icon == "" {
		switch accType {
		case "bank":
			icon = "🏦"
		case "mobile_money":
			icon = "📱"
		case "cash":
			icon = "💵"
		case "savings":
			icon = "💎"
		case "credit":
			icon = "💳"
		default:
			icon = "🏦"
		}
	}
	if currency == "" {
		currency = "₦"
	}

	if isDefault {
		_, _ = s.db.Exec("UPDATE accounts SET is_default = 0 WHERE username = ?", username)
	} else {
		var count int
		_ = s.db.QueryRow("SELECT COUNT(1) FROM accounts WHERE username = ?", username).Scan(&count)
		if count == 0 {
			isDefault = true
		}
	}

	now := time.Now()
	isDefInt := 0
	if isDefault {
		isDefInt = 1
	}

	res, err := s.db.Exec(
		"INSERT INTO accounts (username, name, type, currency, initial_balance, color, icon, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		username, name, accType, currency, initialBalance, color, icon, isDefInt, now,
	)
	if err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	acc := &Account{
		ID:             int(id),
		Username:       username,
		Name:           name,
		Type:           accType,
		Currency:       currency,
		InitialBalance: initialBalance,
		CurrentBalance: initialBalance,
		Color:          color,
		Icon:           icon,
		IsDefault:      isDefault,
		TxCount:        0,
		CreatedAt:      now,
	}
	return acc, nil
}

func (s *DBStore) UpdateAccount(id int, username, name, accType, currency, color, icon string, initialBalance float64, isDefault bool) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("account name is required")
	}
	accType = strings.TrimSpace(accType)
	if accType == "" {
		accType = "bank"
	}
	if color == "" {
		color = "#3B82F6"
	}
	if icon == "" {
		switch accType {
		case "bank":
			icon = "🏦"
		case "mobile_money":
			icon = "📱"
		case "cash":
			icon = "💵"
		case "savings":
			icon = "💎"
		case "credit":
			icon = "💳"
		default:
			icon = "🏦"
		}
	}

	if isDefault {
		_, _ = s.db.Exec("UPDATE accounts SET is_default = 0 WHERE username = ?", username)
	}

	isDefInt := 0
	if isDefault {
		isDefInt = 1
	}

	res, err := s.db.Exec(
		"UPDATE accounts SET name = ?, type = ?, currency = ?, initial_balance = ?, color = ?, icon = ?, is_default = ? WHERE id = ? AND username = ?",
		name, accType, currency, initialBalance, color, icon, isDefInt, id, username,
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, fmt.Errorf("account not found")
	}

	var acc Account
	_ = s.db.QueryRow("SELECT id, username, name, type, currency, initial_balance, color, icon, is_default, created_at FROM accounts WHERE id = ? AND username = ?", id, username).
		Scan(&acc.ID, &acc.Username, &acc.Name, &acc.Type, &acc.Currency, &acc.InitialBalance, &acc.Color, &acc.Icon, &acc.IsDefault, &acc.CreatedAt)

	return &acc, nil
}

func (s *DBStore) DeleteAccount(id int, username string, reassignToAccountID int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int
	_ = s.db.QueryRow("SELECT COUNT(1) FROM accounts WHERE username = ?", username).Scan(&count)
	if count <= 1 {
		return false, fmt.Errorf("cannot delete your only account")
	}

	var isDefault bool
	err := s.db.QueryRow("SELECT is_default FROM accounts WHERE id = ? AND username = ?", id, username).Scan(&isDefault)
	if err != nil {
		return false, fmt.Errorf("account not found")
	}

	if reassignToAccountID <= 0 || reassignToAccountID == id {
		_ = s.db.QueryRow("SELECT id FROM accounts WHERE username = ? AND id != ? ORDER BY is_default DESC, id ASC LIMIT 1", username, id).Scan(&reassignToAccountID)
	}

	if isDefault {
		_, _ = s.db.Exec("UPDATE accounts SET is_default = 1 WHERE id = ? AND username = ?", reassignToAccountID, username)
	}

	// Reassign transactions
	_, _ = s.db.Exec("UPDATE transactions SET account_id = ? WHERE username = ? AND account_id = ?", reassignToAccountID, username, id)

	// Clean up transfers involving this account
	_, _ = s.db.Exec("DELETE FROM account_transfers WHERE username = ? AND (from_account_id = ? OR to_account_id = ?)", username, id, id)

	res, err := s.db.Exec("DELETE FROM accounts WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func (s *DBStore) SetDefaultAccount(id int, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var exists int
	err := s.db.QueryRow("SELECT COUNT(1) FROM accounts WHERE id = ? AND username = ?", id, username).Scan(&exists)
	if err != nil || exists == 0 {
		return fmt.Errorf("account not found")
	}

	_, _ = s.db.Exec("UPDATE accounts SET is_default = 0 WHERE username = ?", username)
	_, err = s.db.Exec("UPDATE accounts SET is_default = 1 WHERE id = ? AND username = ?", id, username)
	return err
}

func (s *DBStore) CreateAccountTransfer(username string, fromID, toID int, amount float64, note string, optDate ...time.Time) (*AccountTransfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if fromID == toID {
		return nil, fmt.Errorf("source and destination accounts cannot be the same")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("transfer amount must be greater than zero")
	}

	var fromName, fromIcon string
	err := s.db.QueryRow("SELECT name, icon FROM accounts WHERE id = ? AND username = ?", fromID, username).Scan(&fromName, &fromIcon)
	if err != nil {
		return nil, fmt.Errorf("source account not found")
	}

	var toName, toIcon string
	err = s.db.QueryRow("SELECT name, icon FROM accounts WHERE id = ? AND username = ?", toID, username).Scan(&toName, &toIcon)
	if err != nil {
		return nil, fmt.Errorf("destination account not found")
	}

	txDate := time.Now()
	if len(optDate) > 0 && !optDate[0].IsZero() {
		txDate = optDate[0]
	}
	now := time.Now()

	res, err := s.db.Exec(
		"INSERT INTO account_transfers (username, from_account_id, to_account_id, amount, note, date, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		username, fromID, toID, amount, strings.TrimSpace(note), txDate, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	return &AccountTransfer{
		ID:              int(id),
		Username:        username,
		FromAccountID:   fromID,
		FromAccountName: fromName,
		FromAccountIcon: fromIcon,
		ToAccountID:     toID,
		ToAccountName:   toName,
		ToAccountIcon:   toIcon,
		Amount:          amount,
		Note:            note,
		Date:            txDate,
		CreatedAt:       now,
	}, nil
}

func (s *DBStore) GetAccountTransfers(username string, limit int) ([]AccountTransfer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(
		`SELECT t.id, t.username, t.from_account_id, COALESCE(fa.name, 'Unknown'), COALESCE(fa.icon, '🏦'),
                t.to_account_id, COALESCE(ta.name, 'Unknown'), COALESCE(ta.icon, '🏦'),
                t.amount, COALESCE(t.note, ''), t.date, t.created_at
         FROM account_transfers t
         LEFT JOIN accounts fa ON fa.id = t.from_account_id AND fa.username = t.username
         LEFT JOIN accounts ta ON ta.id = t.to_account_id AND ta.username = t.username
         WHERE t.username = ?
         ORDER BY t.date DESC, t.id DESC
         LIMIT ?`,
		username, limit,
	)
	if err != nil {
		return []AccountTransfer{}, err
	}
	defer rows.Close()

	var transfers []AccountTransfer
	for rows.Next() {
		var tr AccountTransfer
		if err := rows.Scan(&tr.ID, &tr.Username, &tr.FromAccountID, &tr.FromAccountName, &tr.FromAccountIcon,
			&tr.ToAccountID, &tr.ToAccountName, &tr.ToAccountIcon, &tr.Amount, &tr.Note, &tr.Date, &tr.CreatedAt); err != nil {
			continue
		}
		transfers = append(transfers, tr)
	}
	if transfers == nil {
		transfers = []AccountTransfer{}
	}
	return transfers, nil
}

// ─── Step 6: Smart Budgeting Rules (50/30/20 Rule, Envelope Budgeting, Dynamic Spending Alerts) ───

type BudgetEnvelope struct {
	Category       string  `json:"category"`
	CategoryLabel  string  `json:"category_label"`
	Icon           string  `json:"icon"`
	Color          string  `json:"color"`
	Bucket         string  `json:"bucket"` // "needs", "wants", "savings"
	MonthlyLimit   float64 `json:"monthly_limit"`
	Spent          float64 `json:"spent"`
	Remaining      float64 `json:"remaining"`
	PercentUsed    float64 `json:"percent_used"`
	DailyBudget    float64 `json:"daily_budget"`    // MonthlyLimit / DaysInMonth
	DailySpentAvg  float64 `json:"daily_spent_avg"` // Spent / DayOfMonth
	ProjectedSpend float64 `json:"projected_spend"` // DailySpentAvg * DaysInMonth
	PacePercent    float64 `json:"pace_percent"`    // (ProjectedSpend / MonthlyLimit) * 100
	PaceStatus     string  `json:"pace_status"`     // "on_track", "caution", "warning", "exceeded", "unbudgeted"
	PaceMessage    string  `json:"pace_message"`
}

type Rule503020Bucket struct {
	Name           string   `json:"name"`           // "Needs", "Wants", "Savings"
	Key            string   `json:"key"`            // "needs", "wants", "savings"
	TargetPercent  float64  `json:"target_percent"` // 50, 30, 20
	TargetAmount   float64  `json:"target_amount"`  // Income * (TargetPercent / 100)
	ActualSpent    float64  `json:"actual_spent"`
	ActualPercent  float64  `json:"actual_percent"`  // (ActualSpent / Income) * 100
	VarianceAmount float64  `json:"variance_amount"` // TargetAmount - ActualSpent (positive = safe/under budget)
	Status         string   `json:"status"`          // "on_track", "caution", "over"
	Categories     []string `json:"categories"`
}

type SpendingAlert struct {
	Type     string  `json:"type"` // "danger", "warning", "caution", "info", "success"
	Title    string  `json:"title"`
	Message  string  `json:"message"`
	Category string  `json:"category,omitempty"`
	Pace     float64 `json:"pace,omitempty"`
}

type SmartBudgetReport struct {
	CurrentMonth         string             `json:"current_month"`
	DayOfMonth           int                `json:"day_of_month"`
	DaysInMonth          int                `json:"days_in_month"`
	DaysRemaining        int                `json:"days_remaining"`
	MonthProgressPct     float64            `json:"month_progress_pct"`
	MonthlyIncome        float64            `json:"monthly_income"`
	IncomeSource         string             `json:"income_source"` // "current_month", "average", "estimated"
	TotalExpense         float64            `json:"total_expense"`
	TotalBudgetLimit     float64            `json:"total_budget_limit"`
	TotalBudgetSpent     float64            `json:"total_budget_spent"`
	TotalBudgetRemaining float64            `json:"total_budget_remaining"`
	BudgetProgressPct    float64            `json:"budget_progress_pct"`
	OverallPaceStatus    string             `json:"overall_pace_status"` // "on_track", "caution", "warning", "exceeded"
	Rule503020           []Rule503020Bucket `json:"rule_50_30_20"`
	Envelopes            []BudgetEnvelope   `json:"envelopes"`
	Alerts               []SpendingAlert    `json:"alerts"`
	Currency             string             `json:"currency"`
}

// ClassifyCategoryBucket classifies any category slug/name into Needs (50%), Wants (30%), or Savings (20%).
func ClassifyCategoryBucket(category string) string {
	c := strings.ToLower(strings.TrimSpace(category))
	switch c {
	case "savings", "investment", "investments", "debt", "debt repayment", "crypto", "emergency fund", "vault", "pension", "retirement":
		return "savings"
	case "housing", "rent", "mortgage", "utilities", "bills", "food", "groceries", "supermarket",
		"transport", "transportation", "fuel", "gas", "car", "healthcare", "health", "medical", "pharmacy", "doctor",
		"education", "school", "tuition", "insurance":
		return "needs"
	case "entertainment", "shopping", "personal", "personal care", "dining", "restaurant", "takeout", "travel", "vacation",
		"hobbies", "gifts", "subscriptions", "other", "general", "leisure":
		return "wants"
	}

	// Keyword heuristics for custom user categories
	if strings.Contains(c, "sav") || strings.Contains(c, "invest") || strings.Contains(c, "debt") || strings.Contains(c, "fund") || strings.Contains(c, "crypto") {
		return "savings"
	}
	if strings.Contains(c, "rent") || strings.Contains(c, "hous") || strings.Contains(c, "util") || strings.Contains(c, "bill") ||
		strings.Contains(c, "food") || strings.Contains(c, "grocer") || strings.Contains(c, "trans") || strings.Contains(c, "fuel") ||
		strings.Contains(c, "health") || strings.Contains(c, "medic") || strings.Contains(c, "educat") || strings.Contains(c, "insur") {
		return "needs"
	}
	return "wants"
}

// GetSmartBudgetReport aggregates envelope limits, burn rate pace, 50/30/20 rule breakdown, and proactive spending alerts.
func (s *DBStore) GetSmartBudgetReport(username string) (*SmartBudgetReport, error) {
	currency := s.GetCurrency(username)
	if currency == "" {
		currency = "₦"
	}

	now := time.Now()
	firstOfNextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	lastOfThisMonth := firstOfNextMonth.Add(-time.Nanosecond)
	daysInMonth := lastOfThisMonth.Day()
	dayOfMonth := now.Day()
	if dayOfMonth < 1 {
		dayOfMonth = 1
	}
	daysRemaining := daysInMonth - dayOfMonth
	if daysRemaining < 0 {
		daysRemaining = 0
	}
	monthProgressPct := math.Round((float64(dayOfMonth)/float64(daysInMonth))*1000) / 10

	// 1. Fetch transactions
	txs, err := s.GetTransactions(username)
	if err != nil {
		return nil, err
	}

	var thisMonthIncome, thisMonthExpense float64
	var pastIncomeSum float64
	var pastIncomeMonths int
	categorySpent := make(map[string]float64)

	// Past 3 months tracking for baseline
	monthIncomes := make(map[string]float64)

	for _, t := range txs {
		isThisMonth := t.Date.Year() == now.Year() && t.Date.Month() == now.Month()
		mKey := fmt.Sprintf("%04d-%02d", t.Date.Year(), t.Date.Month())

		if t.Type == "income" {
			monthIncomes[mKey] += t.Amount
			if isThisMonth {
				thisMonthIncome += t.Amount
			}
		} else if t.Type == "expense" && isThisMonth {
			thisMonthExpense += t.Amount
			categorySpent[t.Category] += t.Amount
		}
	}

	for k, v := range monthIncomes {
		if k != fmt.Sprintf("%04d-%02d", now.Year(), now.Month()) {
			pastIncomeSum += v
			pastIncomeMonths++
		}
	}

	effectiveIncome := thisMonthIncome
	incomeSource := "current_month"
	if effectiveIncome <= 0 {
		if pastIncomeMonths > 0 {
			effectiveIncome = pastIncomeSum / float64(pastIncomeMonths)
			incomeSource = "average"
		} else {
			incomeSource = "estimated"
			// Baseline fallback
			effectiveIncome = 250000
		}
	}

	// 2. Fetch category budgets and category metadata
	budgets, _ := s.GetBudgets(username)
	if budgets == nil {
		budgets = make(map[string]float64)
	}
	categories, _ := s.GetCategories(username)

	catMetaMap := make(map[string]Category)
	for _, c := range categories {
		catMetaMap[strings.ToLower(c.Slug)] = c
		catMetaMap[strings.ToLower(c.Name)] = c
	}

	// Collect unique categories with budgets or spending
	catSet := make(map[string]bool)
	for k := range budgets {
		catSet[k] = true
	}
	for k := range categorySpent {
		catSet[k] = true
	}

	var envelopes []BudgetEnvelope
	var totalBudgetLimit, totalBudgetSpent float64

	for catKey := range catSet {
		limit := budgets[catKey]
		spent := categorySpent[catKey]
		bucket := ClassifyCategoryBucket(catKey)

		if limit > 0 {
			totalBudgetLimit += limit
			totalBudgetSpent += spent
		}

		remaining := limit - spent
		var pctUsed float64
		if limit > 0 {
			pctUsed = math.Round((spent/limit)*1000) / 10
		}

		dailyBudget := 0.0
		if limit > 0 {
			dailyBudget = math.Round((limit/float64(daysInMonth))*100) / 100
		}

		dailySpentAvg := math.Round((spent/float64(dayOfMonth))*100) / 100
		projectedSpend := math.Round((dailySpentAvg*float64(daysInMonth))*100) / 100

		pacePercent := 0.0
		if limit > 0 {
			pacePercent = math.Round((projectedSpend/limit)*1000) / 10
		}

		var paceStatus, paceMsg string
		if limit <= 0 {
			paceStatus = "unbudgeted"
			paceMsg = "No monthly limit set"
		} else if spent > limit {
			paceStatus = "exceeded"
			paceMsg = fmt.Sprintf("Exceeded limit by %s%.0f", currency, spent-limit)
		} else if pacePercent > 115 && dayOfMonth >= 2 {
			paceStatus = "warning"
			paceMsg = fmt.Sprintf("Projected to overshoot by %s%.0f (+%.0f%%)", currency, projectedSpend-limit, pacePercent-100)
		} else if pctUsed >= 80 {
			paceStatus = "caution"
			paceMsg = fmt.Sprintf("%s%.0f remaining (%d days left)", currency, remaining, daysRemaining)
		} else {
			paceStatus = "on_track"
			dailyRemaining := 0.0
			if daysRemaining > 0 {
				dailyRemaining = remaining / float64(daysRemaining)
			}
			paceMsg = fmt.Sprintf("On track (%s%.0f/day remaining)", currency, dailyRemaining)
		}

		// Icon and display label
		catLabel := catKey
		catIcon := "🏷️"
		catColor := "#64748B"
		if meta, ok := catMetaMap[strings.ToLower(catKey)]; ok {
			catLabel = meta.Name
			if meta.Emoji != "" {
				catIcon = meta.Emoji
			}
			if meta.Color != "" {
				catColor = meta.Color
			}
		} else {
			// standard fallback
			switch strings.ToLower(catKey) {
			case "food":
				catLabel = "Food & Dining"
				catIcon = "🍔"
				catColor = "#F59E0B"
			case "transport":
				catLabel = "Transportation"
				catIcon = "🚗"
				catColor = "#3B82F6"
			case "housing":
				catLabel = "Housing & Utilities"
				catIcon = "🏠"
				catColor = "#8B5CF6"
			case "entertainment":
				catLabel = "Entertainment"
				catIcon = "🎬"
				catColor = "#EC4899"
			case "shopping":
				catLabel = "Shopping & Retail"
				catIcon = "🛍️"
				catColor = "#10B981"
			case "healthcare":
				catLabel = "Healthcare & Medical"
				catIcon = "💊"
				catColor = "#EF4444"
			case "bills":
				catLabel = "Bills & Subscriptions"
				catIcon = "💡"
				catColor = "#06B6D4"
			case "personal":
				catLabel = "Personal Care"
				catIcon = "✨"
				catColor = "#F43F5E"
			case "savings":
				catLabel = "Savings & Vaults"
				catIcon = "💰"
				catColor = "#14B8A6"
			}
		}

		envelopes = append(envelopes, BudgetEnvelope{
			Category:       catKey,
			CategoryLabel:  catLabel,
			Icon:           catIcon,
			Color:          catColor,
			Bucket:         bucket,
			MonthlyLimit:   limit,
			Spent:          spent,
			Remaining:      remaining,
			PercentUsed:    pctUsed,
			DailyBudget:    dailyBudget,
			DailySpentAvg:  dailySpentAvg,
			ProjectedSpend: projectedSpend,
			PacePercent:    pacePercent,
			PaceStatus:     paceStatus,
			PaceMessage:    paceMsg,
		})
	}

	// Sort envelopes: exceeded first, warning, caution, on_track, unbudgeted
	statusOrder := map[string]int{
		"exceeded":   1,
		"warning":    2,
		"caution":    3,
		"on_track":   4,
		"unbudgeted": 5,
	}
	sort.Slice(envelopes, func(i, j int) bool {
		oI := statusOrder[envelopes[i].PaceStatus]
		oJ := statusOrder[envelopes[j].PaceStatus]
		if oI != oJ {
			return oI < oJ
		}
		return envelopes[i].PercentUsed > envelopes[j].PercentUsed
	})

	totalBudgetRemaining := totalBudgetLimit - totalBudgetSpent
	var budgetProgressPct float64
	if totalBudgetLimit > 0 {
		budgetProgressPct = math.Round((totalBudgetSpent/totalBudgetLimit)*1000) / 10
	}

	overallPaceStatus := "on_track"
	if totalBudgetLimit > 0 {
		if totalBudgetSpent > totalBudgetLimit {
			overallPaceStatus = "exceeded"
		} else if budgetProgressPct > monthProgressPct+15 {
			overallPaceStatus = "warning"
		} else if budgetProgressPct > monthProgressPct+5 {
			overallPaceStatus = "caution"
		}
	}

	// 3. 50/30/20 Rule Breakdown
	targetNeeds := math.Round(effectiveIncome * 0.50)
	targetWants := math.Round(effectiveIncome * 0.30)
	targetSavings := math.Round(effectiveIncome * 0.20)

	var actualNeeds, actualWants, actualSavingsSpent float64
	var needsCats, wantsCats, savingsCats []string

	for catKey, spent := range categorySpent {
		bucket := ClassifyCategoryBucket(catKey)
		switch bucket {
		case "needs":
			actualNeeds += spent
			needsCats = append(needsCats, catKey)
		case "wants":
			actualWants += spent
			wantsCats = append(wantsCats, catKey)
		case "savings":
			actualSavingsSpent += spent
			savingsCats = append(savingsCats, catKey)
		}
	}

	// If net surplus exists (income > expense), unspent funds count towards retained savings
	surplusSavings := 0.0
	if thisMonthIncome > thisMonthExpense {
		surplusSavings = thisMonthIncome - thisMonthExpense
	}
	totalActualSavings := actualSavingsSpent + surplusSavings

	calcPct := func(val float64) float64 {
		if effectiveIncome <= 0 {
			return 0
		}
		return math.Round((val/effectiveIncome)*1000) / 10
	}

	determineStatus := func(spent, target float64, isSavings bool) string {
		if isSavings {
			if spent >= target {
				return "on_track"
			} else if spent >= target*0.7 {
				return "caution"
			}
			return "over" // below target
		}
		if spent > target {
			return "over"
		} else if spent > target*0.85 {
			return "caution"
		}
		return "on_track"
	}

	rule503020 := []Rule503020Bucket{
		{
			Name:           "Needs (50%)",
			Key:            "needs",
			TargetPercent:  50,
			TargetAmount:   targetNeeds,
			ActualSpent:    actualNeeds,
			ActualPercent:  calcPct(actualNeeds),
			VarianceAmount: targetNeeds - actualNeeds,
			Status:         determineStatus(actualNeeds, targetNeeds, false),
			Categories:     needsCats,
		},
		{
			Name:           "Wants (30%)",
			Key:            "wants",
			TargetPercent:  30,
			TargetAmount:   targetWants,
			ActualSpent:    actualWants,
			ActualPercent:  calcPct(actualWants),
			VarianceAmount: targetWants - actualWants,
			Status:         determineStatus(actualWants, targetWants, false),
			Categories:     wantsCats,
		},
		{
			Name:           "Savings & Debt (20%)",
			Key:            "savings",
			TargetPercent:  20,
			TargetAmount:   targetSavings,
			ActualSpent:    totalActualSavings,
			ActualPercent:  calcPct(totalActualSavings),
			VarianceAmount: totalActualSavings - targetSavings,
			Status:         determineStatus(totalActualSavings, targetSavings, true),
			Categories:     savingsCats,
		},
	}

	// 4. Dynamic Spending Alerts
	var alerts []SpendingAlert

	for _, env := range envelopes {
		if env.PaceStatus == "exceeded" {
			alerts = append(alerts, SpendingAlert{
				Type:     "danger",
				Title:    fmt.Sprintf("%s Envelope Exceeded!", env.CategoryLabel),
				Message:  fmt.Sprintf("You have spent %s%.0f, exceeding your %s%.0f monthly limit by %s%.0f.", currency, env.Spent, currency, env.MonthlyLimit, currency, env.Spent-env.MonthlyLimit),
				Category: env.Category,
				Pace:     env.PacePercent,
			})
		} else if env.PaceStatus == "warning" {
			alerts = append(alerts, SpendingAlert{
				Type:     "warning",
				Title:    fmt.Sprintf("%s Burning Faster Than Normal", env.CategoryLabel),
				Message:  fmt.Sprintf("Current burn rate is pacing at %.0f%% of monthly envelope. Projected month-end spend is %s%.0f.", env.PacePercent, currency, env.ProjectedSpend),
				Category: env.Category,
				Pace:     env.PacePercent,
			})
		}
	}

	if calcPct(actualWants) > 35 {
		alerts = append(alerts, SpendingAlert{
			Type:    "warning",
			Title:   "50/30/20 Wants Exceeded",
			Message: fmt.Sprintf("Lifestyle & non-essential spending is currently taking up %.1f%% of your monthly income (target: 30%% max).", calcPct(actualWants)),
		})
	}

	if calcPct(actualNeeds) > 55 {
		alerts = append(alerts, SpendingAlert{
			Type:    "danger",
			Title:   "Essential Needs Above 50%",
			Message: fmt.Sprintf("Essential living expenses are currently consuming %.1f%% of your monthly income (target: 50%%).", calcPct(actualNeeds)),
		})
	}

	if calcPct(totalActualSavings) >= 20 {
		alerts = append(alerts, SpendingAlert{
			Type:    "success",
			Title:   "50/30/20 Savings Goal Achieved!",
			Message: fmt.Sprintf("Fantastic discipline! You have allocated or retained %.1f%% of income into savings & debt payoff this month.", calcPct(totalActualSavings)),
		})
	}

	if len(alerts) == 0 {
		alerts = append(alerts, SpendingAlert{
			Type:    "info",
			Title:   "Budgets On Track",
			Message: "All spending envelopes are pacing normally within current month parameters.",
		})
	}

	return &SmartBudgetReport{
		CurrentMonth:         now.Format("January 2006"),
		DayOfMonth:           dayOfMonth,
		DaysInMonth:          daysInMonth,
		DaysRemaining:        daysRemaining,
		MonthProgressPct:     monthProgressPct,
		MonthlyIncome:        effectiveIncome,
		IncomeSource:         incomeSource,
		TotalExpense:         thisMonthExpense,
		TotalBudgetLimit:     totalBudgetLimit,
		TotalBudgetSpent:     totalBudgetSpent,
		TotalBudgetRemaining: totalBudgetRemaining,
		BudgetProgressPct:    budgetProgressPct,
		OverallPaceStatus:    overallPaceStatus,
		Rule503020:           rule503020,
		Envelopes:            envelopes,
		Alerts:               alerts,
		Currency:             currency,
	}, nil
}

// Apply503020AutoBudget automatically sets category budget caps based on 50/30/20 distribution of monthly income.
func (s *DBStore) Apply503020AutoBudget(username string, baseIncome ...float64) (map[string]float64, error) {
	income := 0.0
	if len(baseIncome) > 0 && baseIncome[0] > 0 {
		income = baseIncome[0]
	} else {
		rep, _ := s.GetSmartBudgetReport(username)
		if rep != nil && rep.MonthlyIncome > 0 {
			income = rep.MonthlyIncome
		} else {
			income = 250000
		}
	}

	needsTarget := income * 0.50
	wantsTarget := income * 0.30
	savingsTarget := income * 0.20

	round500 := func(val float64) float64 {
		return math.Round(val/500) * 500
	}

	autoAllocations := map[string]float64{
		"food":          round500(needsTarget * 0.40),
		"housing":       round500(needsTarget * 0.35),
		"transport":     round500(needsTarget * 0.15),
		"bills":         round500(needsTarget * 0.10),
		"shopping":      round500(wantsTarget * 0.40),
		"entertainment": round500(wantsTarget * 0.35),
		"personal":      round500(wantsTarget * 0.25),
		"savings":       round500(savingsTarget),
	}

	for cat, limit := range autoAllocations {
		if err := s.SetBudget(username, cat, limit); err != nil {
			return nil, err
		}
	}

	return s.GetBudgets(username)
}

// ─── Split Expenses & Settlement Calculator Methods ─────────────────────────

type SplitExpense struct {
	ID           int                `json:"id"`
	Username     string             `json:"username"`
	Title        string             `json:"title"`
	TotalAmount  float64            `json:"total_amount"`
	PayerName    string             `json:"payer_name"`
	PayerIsUser  bool               `json:"payer_is_user"`
	Category     string             `json:"category"`
	Date         time.Time          `json:"date"`
	DateFmt      string             `json:"date_fmt"`
	SplitType    string             `json:"split_type"` // "equal", "exact", "percent"
	Note         string             `json:"note"`
	CreatedAt    time.Time          `json:"created_at"`
	Participants []SplitParticipant `json:"participants"`
	UserShare    float64            `json:"user_share"`
	SettledCount int                `json:"settled_count"`
	TotalCount   int                `json:"total_count"`
	IsSettled    bool               `json:"is_settled"`
}

type SplitParticipant struct {
	ID          int     `json:"id"`
	SplitID     int     `json:"split_id"`
	Username    string  `json:"username"`
	Name        string  `json:"name"`
	ShareAmount float64 `json:"share_amount"`
	IsUser      bool    `json:"is_user"`
	DebtID      *int    `json:"debt_id,omitempty"`
	Status      string  `json:"status"` // "pending", "settled"
}

type SplitParticipantInput struct {
	Name        string  `json:"name"`
	ShareAmount float64 `json:"share_amount"`
	IsUser      bool    `json:"is_user"`
}

type ContactSettlement struct {
	ContactName string  `json:"contact_name"`
	TheyOweYou  float64 `json:"they_owe_you"` // Total unpaid debts where type = 'owing_me'
	YouOweThem  float64 `json:"you_owe_them"` // Total unpaid debts where type = 'i_owe'
	NetAmount   float64 `json:"net_amount"`   // TheyOweYou - YouOweThem
	Status      string  `json:"status"`       // "they_owe", "you_owe", "even"
	ActiveDebts []Debt  `json:"active_debts"`
	DebtIDs     []int   `json:"debt_ids"`
}

type SettlementOverview struct {
	Contacts           []ContactSettlement `json:"contacts"`
	TotalReceivableNet float64             `json:"total_receivable_net"`
	TotalPayableNet    float64             `json:"total_payable_net"`
	OverallNet         float64             `json:"overall_net"`
	ContactsCount      int                 `json:"contacts_count"`
}

type SettlementResult struct {
	ContactName  string  `json:"contact_name"`
	Mode         string  `json:"mode"`
	OffsetAmount float64 `json:"offset_amount"`
	NetSettled   float64 `json:"net_settled"`
	Message      string  `json:"message"`
}

func (s *DBStore) CreateSplitExpense(
	username, title, category, note string,
	totalAmount float64,
	date time.Time,
	payerName string,
	payerIsUser bool,
	splitType string,
	participants []SplitParticipantInput,
	logTx bool,
	accountID int,
) (*SplitExpense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	username = strings.TrimSpace(username)
	title = strings.TrimSpace(title)
	if username == "" || title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if totalAmount <= 0 {
		return nil, fmt.Errorf("total amount must be greater than zero")
	}
	if len(participants) < 2 {
		return nil, fmt.Errorf("at least 2 participants are required to split an expense")
	}

	var sumShares float64
	hasUser := false
	for i, p := range participants {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return nil, fmt.Errorf("all participants must have a name")
		}
		if p.ShareAmount < 0 {
			return nil, fmt.Errorf("participant share cannot be negative")
		}
		sumShares += p.ShareAmount
		if p.IsUser || strings.EqualFold(p.Name, "you") || strings.EqualFold(p.Name, username) {
			participants[i].IsUser = true
			participants[i].Name = "You"
			hasUser = true
		}
	}

	if !hasUser {
		for i, p := range participants {
			if strings.EqualFold(p.Name, "you") {
				participants[i].IsUser = true
				hasUser = true
				break
			}
		}
	}

	if math.Abs(sumShares-totalAmount) > 0.08 {
		return nil, fmt.Errorf("sum of participant shares (%.2f) does not match total amount (%.2f)", sumShares, totalAmount)
	}

	payerName = strings.TrimSpace(payerName)
	if payerName == "" || strings.EqualFold(payerName, "you") || strings.EqualFold(payerName, username) {
		payerName = "You"
		payerIsUser = true
	}

	if splitType == "" {
		splitType = "equal"
	}
	if category == "" {
		category = "General"
	}
	if date.IsZero() {
		date = time.Now()
	}

	payerIsUserInt := 0
	if payerIsUser {
		payerIsUserInt = 1
	}

	createdAt := time.Now()
	res, err := s.db.Exec(
		"INSERT INTO splits (username, title, total_amount, payer_name, payer_is_user, category, date, split_type, note, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		username, title, totalAmount, payerName, payerIsUserInt, category, date, splitType, note, createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create split: %w", err)
	}

	splitID64, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	splitID := int(splitID64)

	var userShare float64
	for _, p := range participants {
		if p.IsUser {
			userShare = p.ShareAmount
		}
	}

	// Insert participants and corresponding IOUs/Debts
	for _, p := range participants {
		isUserInt := 0
		if p.IsUser {
			isUserInt = 1
		}

		var debtID *int
		if payerIsUser && !p.IsUser && p.ShareAmount > 0 {
			resD, errD := s.db.Exec(
				"INSERT INTO debts (username, person_name, amount, amount_paid, type, due_date, note, status, created_at, split_id) VALUES (?, ?, ?, 0, 'owing_me', NULL, ?, 'unpaid', ?, ?)",
				username, p.Name, p.ShareAmount, "Split: "+title, createdAt, splitID,
			)
			if errD == nil {
				dID64, _ := resD.LastInsertId()
				dID := int(dID64)
				debtID = &dID
			}
		} else if !payerIsUser && p.IsUser && p.ShareAmount > 0 {
			resD, errD := s.db.Exec(
				"INSERT INTO debts (username, person_name, amount, amount_paid, type, due_date, note, status, created_at, split_id) VALUES (?, ?, ?, 0, 'i_owe', NULL, ?, 'unpaid', ?, ?)",
				username, payerName, p.ShareAmount, fmt.Sprintf("Split: %s (paid by %s)", title, payerName), createdAt, splitID,
			)
			if errD == nil {
				dID64, _ := resD.LastInsertId()
				dID := int(dID64)
				debtID = &dID
			}
		}

		_, err = s.db.Exec(
			"INSERT INTO split_participants (split_id, username, name, share_amount, is_user, debt_id, status) VALUES (?, ?, ?, ?, ?, ?, 'pending')",
			splitID, username, p.Name, p.ShareAmount, isUserInt, debtID,
		)
		if err != nil {
			log.Printf("failed to insert split participant %s: %v", p.Name, err)
		}
	}

	// Log transaction if requested
	if logTx && payerIsUser && userShare > 0 {
		txNote := fmt.Sprintf("Split: %s (Your share)", title)
		_, _ = s.db.Exec(
			"INSERT INTO transactions (username, amount, category, note, date, type, tags, account_id) VALUES (?, ?, ?, ?, ?, 'expense', 'split', ?)",
			username, userShare, category, txNote, date, accountID,
		)
	}

	return s.getSplitByIDLocked(splitID, username)
}

func (s *DBStore) GetSplits(username string) ([]SplitExpense, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, title, total_amount, payer_name, payer_is_user, category, date, split_type, note, created_at FROM splits WHERE username = ? ORDER BY date DESC, id DESC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var splits []SplitExpense
	for rows.Next() {
		var sp SplitExpense
		var payerIsUserInt int
		if err := rows.Scan(&sp.ID, &sp.Username, &sp.Title, &sp.TotalAmount, &sp.PayerName, &payerIsUserInt, &sp.Category, &sp.Date, &sp.SplitType, &sp.Note, &sp.CreatedAt); err != nil {
			log.Printf("scan split error: %v", err)
			continue
		}
		sp.PayerIsUser = (payerIsUserInt == 1)
		sp.DateFmt = sp.Date.Format("2006-01-02")

		parts, err := s.getSplitParticipantsLocked(sp.ID)
		if err == nil {
			sp.Participants = parts
			settledCount := 0
			for _, p := range parts {
				if p.IsUser {
					sp.UserShare = p.ShareAmount
				}
				if p.Status == "settled" {
					settledCount++
				}
			}
			sp.SettledCount = settledCount
			sp.TotalCount = len(parts)
			sp.IsSettled = (len(parts) > 0 && settledCount == len(parts))
		}

		splits = append(splits, sp)
	}

	return splits, nil
}

func (s *DBStore) GetSplitByID(id int, username string) (*SplitExpense, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getSplitByIDLocked(id, username)
}

func (s *DBStore) getSplitByIDLocked(id int, username string) (*SplitExpense, error) {
	var sp SplitExpense
	var payerIsUserInt int
	err := s.db.QueryRow(
		"SELECT id, username, title, total_amount, payer_name, payer_is_user, category, date, split_type, note, created_at FROM splits WHERE id = ? AND username = ?",
		id, username,
	).Scan(&sp.ID, &sp.Username, &sp.Title, &sp.TotalAmount, &sp.PayerName, &payerIsUserInt, &sp.Category, &sp.Date, &sp.SplitType, &sp.Note, &sp.CreatedAt)
	if err != nil {
		return nil, err
	}
	sp.PayerIsUser = (payerIsUserInt == 1)
	sp.DateFmt = sp.Date.Format("2006-01-02")

	parts, err := s.getSplitParticipantsLocked(sp.ID)
	if err == nil {
		sp.Participants = parts
		settledCount := 0
		for _, p := range parts {
			if p.IsUser {
				sp.UserShare = p.ShareAmount
			}
			if p.Status == "settled" {
				settledCount++
			}
		}
		sp.SettledCount = settledCount
		sp.TotalCount = len(parts)
		sp.IsSettled = (len(parts) > 0 && settledCount == len(parts))
	}

	return &sp, nil
}

func (s *DBStore) getSplitParticipantsLocked(splitID int) ([]SplitParticipant, error) {
	rows, err := s.db.Query(
		"SELECT id, split_id, username, name, share_amount, is_user, debt_id, status FROM split_participants WHERE split_id = ? ORDER BY is_user DESC, id ASC",
		splitID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []SplitParticipant
	for rows.Next() {
		var p SplitParticipant
		var isUserInt int
		var debtID sql.NullInt64
		if err := rows.Scan(&p.ID, &p.SplitID, &p.Username, &p.Name, &p.ShareAmount, &isUserInt, &debtID, &p.Status); err != nil {
			continue
		}
		p.IsUser = (isUserInt == 1)
		if debtID.Valid {
			d := int(debtID.Int64)
			p.DebtID = &d

			var dStatus string
			if err := s.db.QueryRow("SELECT status FROM debts WHERE id = ?", d).Scan(&dStatus); err == nil {
				if dStatus == "settled" {
					p.Status = "settled"
				}
			}
		}
		parts = append(parts, p)
	}
	return parts, nil
}

func (s *DBStore) DeleteSplit(id int, username string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.db.Exec("DELETE FROM debts WHERE split_id = ? AND username = ?", id, username)
	_, _ = s.db.Exec("DELETE FROM split_participants WHERE split_id = ?", id)

	res, err := s.db.Exec("DELETE FROM splits WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// GetSettlementOverview aggregates debts by contact to calculate mutual net balances.
func (s *DBStore) GetSettlementOverview(username string) (*SettlementOverview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, username, person_name, amount, amount_paid, type, due_date, note, status, created_at FROM debts WHERE username = ? AND status != 'settled' ORDER BY person_name ASC, created_at DESC",
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	contactMap := make(map[string]*ContactSettlement)
	var contactOrder []string

	for rows.Next() {
		var d Debt
		var dueDate sql.NullTime
		if err := rows.Scan(&d.ID, &d.Username, &d.PersonName, &d.Amount, &d.AmountPaid, &d.Type, &dueDate, &d.Note, &d.Status, &d.CreatedAt); err != nil {
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

		cName := strings.TrimSpace(d.PersonName)
		lookupKey := strings.ToLower(cName)

		cs, exists := contactMap[lookupKey]
		if !exists {
			cs = &ContactSettlement{
				ContactName: cName,
			}
			contactMap[lookupKey] = cs
			contactOrder = append(contactOrder, lookupKey)
		}

		if d.Type == "owing_me" {
			cs.TheyOweYou += rem
		} else {
			cs.YouOweThem += rem
		}
		cs.ActiveDebts = append(cs.ActiveDebts, d)
		cs.DebtIDs = append(cs.DebtIDs, d.ID)
	}

	overview := &SettlementOverview{
		Contacts: make([]ContactSettlement, 0),
	}

	for _, key := range contactOrder {
		cs := contactMap[key]
		cs.NetAmount = cs.TheyOweYou - cs.YouOweThem

		if cs.NetAmount > 0.005 {
			cs.Status = "they_owe"
			overview.TotalReceivableNet += cs.NetAmount
		} else if cs.NetAmount < -0.005 {
			cs.Status = "you_owe"
			overview.TotalPayableNet += math.Abs(cs.NetAmount)
		} else {
			cs.Status = "even"
		}

		overview.Contacts = append(overview.Contacts, *cs)
	}

	overview.OverallNet = overview.TotalReceivableNet - overview.TotalPayableNet
	overview.ContactsCount = len(overview.Contacts)

	sort.Slice(overview.Contacts, func(i, j int) bool {
		return math.Abs(overview.Contacts[i].NetAmount) > math.Abs(overview.Contacts[j].NetAmount)
	})

	return overview, nil
}

// SettleContactDebts simplifies or completes settlement with a contact.
func (s *DBStore) SettleContactDebts(username, contactName, mode string, accountID int, logTx bool) (*SettlementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	contactName = strings.TrimSpace(contactName)
	if contactName == "" {
		return nil, fmt.Errorf("contact name is required")
	}

	rows, err := s.db.Query(
		"SELECT id, amount, amount_paid, type FROM debts WHERE username = ? AND LOWER(TRIM(person_name)) = LOWER(?) AND status != 'settled'",
		username, contactName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type activeItem struct {
		id         int
		amount     float64
		amountPaid float64
		rem        float64
		debtType   string
	}

	var owingMeList []activeItem
	var iOweList []activeItem
	var totalOwingMe, totalIOwe float64

	for rows.Next() {
		var it activeItem
		if err := rows.Scan(&it.id, &it.amount, &it.amountPaid, &it.debtType); err != nil {
			continue
		}
		it.rem = it.amount - it.amountPaid
		if it.rem <= 0 {
			continue
		}
		if it.debtType == "owing_me" {
			owingMeList = append(owingMeList, it)
			totalOwingMe += it.rem
		} else {
			iOweList = append(iOweList, it)
			totalIOwe += it.rem
		}
	}

	currency := "₦"
	var userCurr string
	if err := s.db.QueryRow("SELECT currency FROM users WHERE username = ?", username).Scan(&userCurr); err == nil && userCurr != "" {
		currency = userCurr
	}

	res := &SettlementResult{
		ContactName: contactName,
		Mode:        mode,
	}

	if mode == "offset_only" {
		offset := math.Min(totalOwingMe, totalIOwe)
		if offset <= 0.005 {
			return nil, fmt.Errorf("no mutual debts to offset with %s (debts are only in one direction)", contactName)
		}

		remOffset := offset
		for _, it := range owingMeList {
			if remOffset <= 0 {
				break
			}
			deduct := math.Min(it.rem, remOffset)
			newPaid := it.amountPaid + deduct
			newStatus := "partial"
			if newPaid >= it.amount-0.005 {
				newPaid = it.amount
				newStatus = "settled"
			}
			_, _ = s.db.Exec("UPDATE debts SET amount_paid = ?, status = ? WHERE id = ?", newPaid, newStatus, it.id)
			remOffset -= deduct
		}

		remOffset = offset
		for _, it := range iOweList {
			if remOffset <= 0 {
				break
			}
			deduct := math.Min(it.rem, remOffset)
			newPaid := it.amountPaid + deduct
			newStatus := "partial"
			if newPaid >= it.amount-0.005 {
				newPaid = it.amount
				newStatus = "settled"
			}
			_, _ = s.db.Exec("UPDATE debts SET amount_paid = ?, status = ? WHERE id = ?", newPaid, newStatus, it.id)
			remOffset -= deduct
		}

		res.OffsetAmount = offset
		res.Message = fmt.Sprintf("Successfully simplified debts! Offset %s%.2f of mutual debt with %s.", currency, offset, contactName)
		return res, nil
	}

	// Full Settlement Mode
	net := totalOwingMe - totalIOwe
	res.NetSettled = net

	for _, it := range append(owingMeList, iOweList...) {
		_, _ = s.db.Exec("UPDATE debts SET amount_paid = amount, status = 'settled' WHERE id = ?", it.id)
	}

	_, _ = s.db.Exec(
		"UPDATE split_participants SET status = 'settled' WHERE username = ? AND LOWER(TRIM(name)) = LOWER(?)",
		username, contactName,
	)

	if logTx && math.Abs(net) > 0.005 {
		now := time.Now()
		if net > 0 {
			_, _ = s.db.Exec(
				"INSERT INTO transactions (username, amount, category, note, date, type, tags, account_id) VALUES (?, ?, 'Debt Repayment', ?, ?, 'income', 'settlement', ?)",
				username, net, fmt.Sprintf("Settlement received from %s", contactName), now, accountID,
			)
		} else {
			_, _ = s.db.Exec(
				"INSERT INTO transactions (username, amount, category, note, date, type, tags, account_id) VALUES (?, ?, 'Debt Repayment', ?, ?, 'expense', 'settlement', ?)",
				username, -net, fmt.Sprintf("Settlement paid to %s", contactName), now, accountID,
			)
		}
	}

	if net > 0.005 {
		res.Message = fmt.Sprintf("All settled! %s paid you %s%.2f. Account is now completely squared up.", contactName, currency, net)
	} else if net < -0.005 {
		res.Message = fmt.Sprintf("All settled! You paid %s %s%.2f. Account is now completely squared up.", contactName, currency, -net)
	} else {
		res.Message = fmt.Sprintf("All debts with %s were fully offset and squared up to %s0.00.", contactName, currency)
	}

	return res, nil
}
