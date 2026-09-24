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
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Migrate existing users table if columns don't exist
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN email TEXT")
	_, _ = s.db.Exec("ALTER TABLE users ADD COLUMN full_name TEXT DEFAULT ''")
	_, _ = s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email)")

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

	_, err = s.db.Exec("INSERT INTO users (username, full_name, email, password, currency, created_at) VALUES (?, ?, ?, ?, ?, '₦', ?)",
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
