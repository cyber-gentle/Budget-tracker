package app

import (
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*
var embeddedFS embed.FS

// App holds the shared application state and routing.
type App struct {
	DB   *DBStore
	Tmpl *template.Template
	Mux  *http.ServeMux
}

// NewApp creates an App, parses templates, and registers all routes.
func NewApp(db *DBStore) *App {
	tmpl := template.Must(template.ParseFS(embeddedFS, "templates/html/*.html"))

	app := &App{
		DB:   db,
		Tmpl: tmpl,
		Mux:  http.NewServeMux(),
	}

	app.routes()
	return app
}

// ServeHTTP delegates to the internal mux (implements http.Handler).
func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	app.Mux.ServeHTTP(w, r)
}

func (app *App) routes() {
	// Static assets from embedded FS
	staticFS, err := fs.Sub(embeddedFS, "templates")
	if err != nil {
		log.Fatalf("failed to create static sub fs: %v", err)
	}
	app.Mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Favicon
	app.Mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		data, err := embeddedFS.ReadFile("templates/assets/imgs/favicon.svg")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	})

	// Pages
	app.Mux.HandleFunc("/", app.HandleHome)
	app.Mux.HandleFunc("/login", app.HandleLogin)
	app.Mux.HandleFunc("/sign-up", app.HandleSignUp)
	app.Mux.HandleFunc("/dashboard", app.HandleDashboard)
	app.Mux.HandleFunc("/debts", app.HandleDebts)
	app.Mux.HandleFunc("/subscriptions", app.HandleSubscriptionsPage)
	app.Mux.HandleFunc("/goals", app.HandleGoalsPage)

	// Auth APIs
	app.Mux.HandleFunc("/api/signup", app.HandleSignupAPI)
	app.Mux.HandleFunc("/api/login", app.HandleLoginAPI)
	app.Mux.HandleFunc("/api/logout", app.HandleLogoutAPI)

	// Transaction APIs
	app.Mux.HandleFunc("/api/transactions", app.HandleTransactions)
	app.Mux.HandleFunc("/api/transactions/", app.HandleTransactionByID)

	// Debts & IOUs APIs
	app.Mux.HandleFunc("/api/debts", app.HandleDebtsAPI)
	app.Mux.HandleFunc("/api/debts/", app.HandleDebtByID)

	// Subscriptions & Recurring Bills APIs
	app.Mux.HandleFunc("/api/subscriptions", app.HandleSubscriptionsAPI)
	app.Mux.HandleFunc("/api/subscriptions/", app.HandleSubscriptionByID)

	// Savings Goals & Virtual Pots APIs
	app.Mux.HandleFunc("/api/goals", app.HandleGoalsAPI)
	app.Mux.HandleFunc("/api/goals/", app.HandleGoalByID)

	// Custom Categories & Tags APIs
	app.Mux.HandleFunc("/api/categories", app.HandleCategoriesAPI)
	app.Mux.HandleFunc("/api/categories/", app.HandleCategoryByID)
	app.Mux.HandleFunc("/api/tags", app.HandleTagsAPI)

	// Budgets, Analytics, Export, Currency APIs
	app.Mux.HandleFunc("/api/budgets", app.HandleBudgets)
	app.Mux.HandleFunc("/api/analytics", app.HandleAnalytics)
	app.Mux.HandleFunc("/api/export", app.HandleExportCSV)
	app.Mux.HandleFunc("/api/currency", app.HandleCurrencyAPI)
	app.Mux.HandleFunc("/api/profile", app.HandleProfileAPI)

	// Health check
	app.Mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

// renderTemplate executes an embedded template.
func (app *App) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.Tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("error rendering template %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// getSessionUser extracts the username from the session cookie, if valid.
func (app *App) getSessionUser(r *http.Request) (string, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		return "", false
	}
	return app.DB.ValidateSession(c.Value)
}

// ─── Page Handlers ──────────────────────────────────────────────────────────

func (app *App) HandleHome(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/", "", "/index", "/index.html", "/api", "/api/index", "/api/index/":
		if _, ok := app.getSessionUser(r); ok {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		app.renderTemplate(w, "index.html", nil)
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func (app *App) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := app.getSessionUser(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	app.renderTemplate(w, "login.html", nil)
}

func (app *App) HandleSignUp(w http.ResponseWriter, r *http.Request) {
	if _, ok := app.getSessionUser(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	app.renderTemplate(w, "sign-up.html", nil)
}

func (app *App) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := app.DB.GetCurrency(username)
	income, expenses, balance, _ := app.DB.CalculateTotals(username)

	savingsRate := 0
	if income > 0 {
		rate := int(((income - expenses) / income) * 100)
		if rate < 0 {
			rate = 0
		}
		savingsRate = rate
	}

	prof, _ := app.DB.GetProfile(username)
	fullName := ""
	email := ""
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
	}

	data := struct {
		Username    string
		FullName    string
		Email       string
		Currency    string
		IncomeFmt   string
		ExpensesFmt string
		BalanceFmt  string
		SavingsRate int
	}{
		Username:    username,
		FullName:    fullName,
		Email:       email,
		Currency:    curr,
		IncomeFmt:   formatMoney(income, curr),
		ExpensesFmt: formatMoney(expenses, curr),
		BalanceFmt:  formatMoney(balance, curr),
		SavingsRate: savingsRate,
	}

	app.renderTemplate(w, "dashboard.html", data)
}

// ─── Auth API Handlers ─────────────────────────────────────────────────────

func (app *App) HandleSignupAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(1 << 20)
	fullName := strings.TrimSpace(r.FormValue("name"))
	if fullName == "" {
		fullName = strings.TrimSpace(r.FormValue("full_name"))
	}
	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	if err := app.DB.Signup(username, email, password, fullName); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	token, err := app.DB.Login(username, password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 86400,
	})
	jsonOK(w, map[string]string{"redirect": "/dashboard"})
}

func (app *App) HandleLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(1 << 20)
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("username"))
	}
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("email"))
	}
	password := r.FormValue("password")

	token, err := app.DB.Login(identifier, password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 86400,
	})
	jsonOK(w, map[string]string{"redirect": "/dashboard"})
}

func (app *App) HandleLogoutAPI(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("session")
	if err == nil {
		app.DB.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ─── Transaction API Handlers ───────────────────────────────────────────────

func (app *App) HandleTransactions(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		txs, err := app.DB.GetTransactions(username)
		if err != nil {
			jsonError(w, "failed to fetch transactions", http.StatusInternalServerError)
			return
		}
		if txs == nil {
			txs = []Transaction{}
		}
		jsonOK(w, txs)
	case http.MethodPost:
		r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		note := strings.TrimSpace(r.FormValue("note"))
		txnType := strings.TrimSpace(r.FormValue("type"))
		dateStr := strings.TrimSpace(r.FormValue("date"))

		if amountStr == "" || txnType == "" {
			jsonError(w, "amount and type are required", http.StatusBadRequest)
			return
		}
		if category == "" {
			category = "other"
		}
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		txDate := time.Now()
		if dateStr != "" {
			if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
				now := time.Now()
				txDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local)
			}
		}

		tags := strings.TrimSpace(r.FormValue("tags"))
		if err := app.DB.AddTransactionWithTags(username, amount, category, note, txnType, tags, txDate); err != nil {
			jsonError(w, "failed to save transaction", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleTransactionByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	id, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		note := strings.TrimSpace(r.FormValue("note"))
		txnType := strings.TrimSpace(r.FormValue("type"))
		dateStr := strings.TrimSpace(r.FormValue("date"))

		if amountStr == "" || txnType == "" {
			jsonError(w, "amount and type are required", http.StatusBadRequest)
			return
		}
		if category == "" {
			category = "other"
		}
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		var optDate []time.Time
		if dateStr != "" {
			if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
				now := time.Now()
				optDate = append(optDate, time.Date(parsed.Year(), parsed.Month(), parsed.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local))
			}
		}

		tags := strings.TrimSpace(r.FormValue("tags"))
		updated, err := app.DB.UpdateTransactionWithTags(id, username, amount, category, note, txnType, tags, optDate...)
		if err != nil || !updated {
			jsonError(w, "transaction not found or update failed", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "updated"})
	case http.MethodDelete:
		deleted, err := app.DB.DeleteTransaction(id, username)
		if err != nil || !deleted {
			jsonError(w, "transaction not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Budgets API Handlers ───────────────────────────────────────────────────

func (app *App) HandleBudgets(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		budgets, _ := app.DB.GetBudgets(username)
		spending, _ := app.DB.GetCurrentMonthSpending(username)
		jsonOK(w, map[string]any{
			"budgets":  budgets,
			"spending": spending,
			"currency": app.DB.GetCurrency(username),
		})
	case http.MethodPost:
		r.ParseMultipartForm(1 << 20)
		category := strings.TrimSpace(r.FormValue("category"))
		limitStr := strings.TrimSpace(r.FormValue("limit"))
		if category == "" {
			jsonError(w, "category is required", http.StatusBadRequest)
			return
		}
		limit, err := strconv.ParseFloat(limitStr, 64)
		if err != nil {
			jsonError(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if err := app.DB.SetBudget(username, category, limit); err != nil {
			jsonError(w, "failed to set budget", http.StatusInternalServerError)
			return
		}
		budgets, _ := app.DB.GetBudgets(username)
		spending, _ := app.DB.GetCurrentMonthSpending(username)
		jsonOK(w, map[string]any{
			"status":   "ok",
			"budgets":  budgets,
			"spending": spending,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Analytics API Handlers ─────────────────────────────────────────────────

func (app *App) HandleAnalytics(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	txs, _ := app.DB.GetTransactions(username)
	now := time.Now()
	var thisMonthIncome, thisMonthExpense float64
	for _, t := range txs {
		if t.Date.Year() == now.Year() && t.Date.Month() == now.Month() {
			if t.Type == "income" {
				thisMonthIncome += t.Amount
			} else {
				thisMonthExpense += t.Amount
			}
		}
	}

	savingsRate := 0
	if thisMonthIncome > 0 {
		rate := int(((thisMonthIncome - thisMonthExpense) / thisMonthIncome) * 100)
		if rate < 0 {
			rate = 0
		}
		savingsRate = rate
	}

	timeframe := strings.TrimSpace(r.URL.Query().Get("timeframe"))
	if timeframe == "" {
		if m := strings.TrimSpace(r.URL.Query().Get("months")); m != "" {
			timeframe = m + "m"
		} else {
			timeframe = "6m"
		}
	}
	start := strings.TrimSpace(r.URL.Query().Get("start"))
	end := strings.TrimSpace(r.URL.Query().Get("end"))

	income, expense, balance, _ := app.DB.CalculateTotals(username)
	breakdown, _ := app.DB.CategoryBreakdown(username, false)
	trends, _ := app.DB.GetTrendsByTimeFrame(username, timeframe, start, end)

	var trendIncome, trendExpense float64
	for _, tr := range trends {
		trendIncome += tr.Income
		trendExpense += tr.Expense
	}

	jsonOK(w, map[string]any{
		"category_breakdown": breakdown,
		"trends":             trends,
		"timeframe":          timeframe,
		"trend_summary": map[string]any{
			"income":  trendIncome,
			"expense": trendExpense,
			"net":     trendIncome - trendExpense,
		},
		"summary": map[string]any{
			"total_income":       income,
			"total_expense":      expense,
			"balance":            balance,
			"this_month_income":  thisMonthIncome,
			"this_month_expense": thisMonthExpense,
			"savings_rate":       savingsRate,
			"currency":           app.DB.GetCurrency(username),
		},
	})
}

// ─── CSV Export API Handler ─────────────────────────────────────────────────

func (app *App) HandleExportCSV(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	txs, err := app.DB.GetTransactions(username)
	if err != nil {
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"spendly_%s_transactions.csv\"", username))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	writer.Write([]string{"ID", "Date", "Type", "Category", "Amount", "Note"})
	for _, t := range txs {
		writer.Write([]string{
			strconv.Itoa(t.ID),
			t.Date.Format("2006-01-02 15:04:05"),
			t.Type,
			t.Category,
			fmt.Sprintf("%.2f", t.Amount),
			t.Note,
		})
	}
}

// ─── Currency API Handler ───────────────────────────────────────────────────

func (app *App) HandleCurrencyAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(1 << 20)
	curr := strings.TrimSpace(r.FormValue("currency"))
	if curr == "" {
		curr = "₦"
	}
	_ = app.DB.SetCurrency(username, curr)
	jsonOK(w, map[string]string{"currency": curr})
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func formatMoney(v float64, symbol string) string {
	if symbol == "" {
		symbol = "₦"
	}
	return fmt.Sprintf("%s%s", symbol, comma(int(v)))
}

func comma(n int) string {
	isNeg := false
	if n < 0 {
		isNeg = true
		n = -n
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		if isNeg {
			return "-" + s
		}
		return s
	}
	var parts []string
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}
	res := strings.Join(parts, ",")
	if isNeg {
		res = "-" + res
	}
	return res
}

func jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Profile API Handler ───────────────────────────────────────────────────

func (app *App) HandleProfileAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		prof, err := app.DB.GetProfile(username)
		if err != nil {
			jsonError(w, "failed to get profile", http.StatusInternalServerError)
			return
		}
		jsonOK(w, prof)

	case http.MethodPost, http.MethodPut:
		_ = r.ParseMultipartForm(1 << 20)
		fullName := strings.TrimSpace(r.FormValue("full_name"))
		email := strings.TrimSpace(r.FormValue("email"))
		currency := strings.TrimSpace(r.FormValue("currency"))
		currentPass := r.FormValue("current_password")
		newPass := r.FormValue("new_password")

		if err := app.DB.UpdateProfile(username, fullName, email, currency, currentPass, newPass); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		prof, _ := app.DB.GetProfile(username)
		jsonOK(w, map[string]any{
			"status": "ok",
			"user":   prof,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Debts & IOUs Handlers ──────────────────────────────────────────────────

func (app *App) HandleDebts(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := app.DB.GetCurrency(username)
	prof, _ := app.DB.GetProfile(username)
	fullName := ""
	email := ""
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
	}

	data := struct {
		Username string
		FullName string
		Email    string
		Currency string
	}{
		Username: username,
		FullName: fullName,
		Email:    email,
		Currency: curr,
	}

	app.renderTemplate(w, "debts.html", data)
}

func (app *App) HandleDebtsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		debts, summary, err := app.DB.GetDebts(username)
		if err != nil {
			jsonError(w, "failed to get debts", http.StatusInternalServerError)
			return
		}
		if debts == nil {
			debts = []Debt{}
		}
		jsonOK(w, map[string]any{
			"debts":   debts,
			"summary": summary,
		})

	case http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		personName := strings.TrimSpace(r.FormValue("person_name"))
		debtType := strings.TrimSpace(r.FormValue("type"))
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		dueDateStr := strings.TrimSpace(r.FormValue("due_date"))
		note := strings.TrimSpace(r.FormValue("note"))

		if personName == "" || amountStr == "" {
			jsonError(w, "person name and amount are required", http.StatusBadRequest)
			return
		}

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		var dueDate *time.Time
		if dueDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", dueDateStr); err == nil {
				dueDate = &parsed
			}
		}

		debt, err := app.DB.CreateDebt(username, personName, debtType, amount, dueDate, note)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, debt)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleDebtByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/debts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonError(w, "invalid debt id", http.StatusBadRequest)
		return
	}

	// Check if this is /api/debts/{id}/pay
	if len(parts) == 2 && parts[1] == "pay" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		_ = r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		paymentDateStr := strings.TrimSpace(r.FormValue("date"))
		logTxnStr := strings.TrimSpace(r.FormValue("log_transaction"))

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid payment amount", http.StatusBadRequest)
			return
		}

		paymentDate := time.Now()
		if paymentDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", paymentDateStr); err == nil {
				now := time.Now()
				paymentDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local)
			}
		}

		logTxn := true
		if logTxnStr == "false" || logTxnStr == "0" {
			logTxn = false
		}

		debt, err := app.DB.RecordDebtPayment(id, username, amount, paymentDate, logTxn)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		jsonOK(w, map[string]any{
			"status": "payment_recorded",
			"debt":   debt,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		debt, err := app.DB.GetDebtByID(id, username)
		if err != nil {
			jsonError(w, "debt not found", http.StatusNotFound)
			return
		}
		jsonOK(w, debt)

	case http.MethodPut, http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		personName := strings.TrimSpace(r.FormValue("person_name"))
		debtType := strings.TrimSpace(r.FormValue("type"))
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		dueDateStr := strings.TrimSpace(r.FormValue("due_date"))
		note := strings.TrimSpace(r.FormValue("note"))

		if personName == "" || amountStr == "" {
			jsonError(w, "person name and amount are required", http.StatusBadRequest)
			return
		}

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		var dueDate *time.Time
		if dueDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", dueDateStr); err == nil {
				dueDate = &parsed
			}
		}

		if err := app.DB.UpdateDebt(id, username, personName, debtType, amount, dueDate, note); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		debt, _ := app.DB.GetDebtByID(id, username)
		jsonOK(w, debt)

	case http.MethodDelete:
		deleted, err := app.DB.DeleteDebt(id, username)
		if err != nil || !deleted {
			jsonError(w, "debt not found or delete failed", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Subscriptions & Recurring Bills Handlers ───────────────────────────────

func (app *App) HandleSubscriptionsPage(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := app.DB.GetCurrency(username)
	prof, _ := app.DB.GetProfile(username)
	fullName := ""
	email := ""
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
	}

	data := struct {
		Username string
		FullName string
		Email    string
		Currency string
	}{
		Username: username,
		FullName: fullName,
		Email:    email,
		Currency: curr,
	}

	app.renderTemplate(w, "subscriptions.html", data)
}

func (app *App) HandleSubscriptionsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	curr := app.DB.GetCurrency(username)

	switch r.Method {
	case http.MethodGet:
		subs, err := app.DB.GetSubscriptions(username)
		if err != nil {
			jsonError(w, "failed to get subscriptions", http.StatusInternalServerError)
			return
		}
		for i := range subs {
			subs[i].AmountFmt = formatMoney(subs[i].Amount, curr)
		}
		commitment, _ := app.DB.GetMonthlyCommitment(username)

		jsonOK(w, map[string]any{
			"subscriptions":          subs,
			"monthly_commitment":     commitment,
			"monthly_commitment_fmt": formatMoney(commitment, curr),
			"count":                  len(subs),
			"currency":               curr,
		})

	case http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		billingCycle := strings.TrimSpace(r.FormValue("billing_cycle"))
		nextDueDateStr := strings.TrimSpace(r.FormValue("next_due_date"))

		if name == "" || amountStr == "" {
			jsonError(w, "subscription name and amount are required", http.StatusBadRequest)
			return
		}

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		nextDueDate := time.Now()
		if nextDueDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", nextDueDateStr); err == nil {
				nextDueDate = parsed
			}
		}

		sub, err := app.DB.AddSubscription(username, name, amount, category, billingCycle, nextDueDate)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		sub.AmountFmt = formatMoney(sub.Amount, curr)

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, sub)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleSubscriptionByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/subscriptions/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonError(w, "invalid subscription id", http.StatusBadRequest)
		return
	}

	curr := app.DB.GetCurrency(username)

	// Check for /api/subscriptions/{id}/pay
	if len(parts) == 2 && parts[1] == "pay" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sub, err := app.DB.PaySubscription(id, username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		sub.AmountFmt = formatMoney(sub.Amount, curr)

		_, _, balance, _ := app.DB.CalculateTotals(username)
		commitment, _ := app.DB.GetMonthlyCommitment(username)

		jsonOK(w, map[string]any{
			"status":                 "paid",
			"subscription":           sub,
			"balance":                balance,
			"balance_fmt":            formatMoney(balance, curr),
			"monthly_commitment":     commitment,
			"monthly_commitment_fmt": formatMoney(commitment, curr),
		})
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		billingCycle := strings.TrimSpace(r.FormValue("billing_cycle"))
		nextDueDateStr := strings.TrimSpace(r.FormValue("next_due_date"))
		status := strings.TrimSpace(r.FormValue("status"))

		if name == "" || amountStr == "" {
			jsonError(w, "subscription name and amount are required", http.StatusBadRequest)
			return
		}

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}

		nextDueDate := time.Now()
		if nextDueDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", nextDueDateStr); err == nil {
				nextDueDate = parsed
			}
		}

		sub, err := app.DB.UpdateSubscription(id, username, name, amount, category, billingCycle, nextDueDate, status)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		sub.AmountFmt = formatMoney(sub.Amount, curr)
		jsonOK(w, sub)

	case http.MethodDelete:
		deleted, err := app.DB.DeleteSubscription(id, username)
		if err != nil || !deleted {
			jsonError(w, "subscription not found or delete failed", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Savings Goals & Virtual Pots Handlers ──────────────────────────────────

func (app *App) HandleGoalsPage(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := app.DB.GetCurrency(username)
	prof, _ := app.DB.GetProfile(username)
	fullName := ""
	email := ""
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
	}

	data := struct {
		Username string
		FullName string
		Email    string
		Currency string
	}{
		Username: username,
		FullName: fullName,
		Email:    email,
		Currency: curr,
	}

	app.renderTemplate(w, "goals.html", data)
}

func (app *App) HandleGoalsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	curr := app.DB.GetCurrency(username)

	switch r.Method {
	case http.MethodGet:
		goals, summary, err := app.DB.GetGoals(username)
		if err != nil {
			jsonError(w, "failed to get savings goals", http.StatusInternalServerError)
			return
		}
		for i := range goals {
			goals[i].TargetFmt = formatMoney(goals[i].TargetAmount, curr)
			goals[i].SavedFmt = formatMoney(goals[i].SavedAmount, curr)
			goals[i].RemainingFmt = formatMoney(goals[i].Remaining, curr)
		}
		summary.TotalSavedFmt = formatMoney(summary.TotalSaved, curr)
		summary.TotalTargetFmt = formatMoney(summary.TotalTarget, curr)
		summary.TotalRemainFmt = formatMoney(summary.TotalRemaining, curr)

		jsonOK(w, map[string]any{
			"goals":    goals,
			"summary":  summary,
			"currency": curr,
		})

	case http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		targetAmountStr := strings.TrimSpace(r.FormValue("target_amount"))
		targetDateStr := strings.TrimSpace(r.FormValue("target_date"))
		emoji := strings.TrimSpace(r.FormValue("emoji"))
		color := strings.TrimSpace(r.FormValue("color"))
		category := strings.TrimSpace(r.FormValue("category"))

		if name == "" || targetAmountStr == "" {
			jsonError(w, "goal name and target amount are required", http.StatusBadRequest)
			return
		}

		targetAmount, err := strconv.ParseFloat(targetAmountStr, 64)
		if err != nil || targetAmount <= 0 {
			jsonError(w, "target amount must be greater than zero", http.StatusBadRequest)
			return
		}

		var targetDate *time.Time
		if targetDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", targetDateStr); err == nil {
				targetDate = &parsed
			}
		}

		goal, err := app.DB.CreateGoal(username, name, targetAmount, targetDate, emoji, color, category)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal.TargetFmt = formatMoney(goal.TargetAmount, curr)
		goal.SavedFmt = formatMoney(goal.SavedAmount, curr)
		goal.RemainingFmt = formatMoney(goal.Remaining, curr)

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, goal)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleGoalByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/goals/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonError(w, "invalid goal id", http.StatusBadRequest)
		return
	}

	curr := app.DB.GetCurrency(username)

	// Sub-route: /api/goals/{id}/deposit
	if len(parts) == 2 && parts[1] == "deposit" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		note := strings.TrimSpace(r.FormValue("note"))
		logTx := r.FormValue("log_transaction") == "true" || r.FormValue("log_transaction") == "1"

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "deposit amount must be greater than zero", http.StatusBadRequest)
			return
		}

		goal, err := app.DB.DepositToGoal(id, username, amount, note, logTx)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal.TargetFmt = formatMoney(goal.TargetAmount, curr)
		goal.SavedFmt = formatMoney(goal.SavedAmount, curr)
		goal.RemainingFmt = formatMoney(goal.Remaining, curr)

		_, _, balance, _ := app.DB.CalculateTotals(username)

		jsonOK(w, map[string]any{
			"status":      "deposited",
			"goal":        goal,
			"balance":     balance,
			"balance_fmt": formatMoney(balance, curr),
		})
		return
	}

	// Sub-route: /api/goals/{id}/withdraw
	if len(parts) == 2 && parts[1] == "withdraw" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		note := strings.TrimSpace(r.FormValue("note"))
		logTx := r.FormValue("log_transaction") == "true" || r.FormValue("log_transaction") == "1"

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "withdrawal amount must be greater than zero", http.StatusBadRequest)
			return
		}

		goal, err := app.DB.WithdrawFromGoal(id, username, amount, note, logTx)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal.TargetFmt = formatMoney(goal.TargetAmount, curr)
		goal.SavedFmt = formatMoney(goal.SavedAmount, curr)
		goal.RemainingFmt = formatMoney(goal.Remaining, curr)

		_, _, balance, _ := app.DB.CalculateTotals(username)

		jsonOK(w, map[string]any{
			"status":      "withdrawn",
			"goal":        goal,
			"balance":     balance,
			"balance_fmt": formatMoney(balance, curr),
		})
		return
	}

	// Root goal item: /api/goals/{id}
	switch r.Method {
	case http.MethodGet:
		goal, contributions, err := app.DB.GetGoalByID(id, username)
		if err != nil {
			jsonError(w, "goal not found", http.StatusNotFound)
			return
		}
		goal.TargetFmt = formatMoney(goal.TargetAmount, curr)
		goal.SavedFmt = formatMoney(goal.SavedAmount, curr)
		goal.RemainingFmt = formatMoney(goal.Remaining, curr)
		for i := range contributions {
			contributions[i].AmountFmt = formatMoney(contributions[i].Amount, curr)
		}

		jsonOK(w, map[string]any{
			"goal":          goal,
			"contributions": contributions,
			"currency":      curr,
		})

	case http.MethodPut, http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		targetAmountStr := strings.TrimSpace(r.FormValue("target_amount"))
		targetDateStr := strings.TrimSpace(r.FormValue("target_date"))
		emoji := strings.TrimSpace(r.FormValue("emoji"))
		color := strings.TrimSpace(r.FormValue("color"))
		category := strings.TrimSpace(r.FormValue("category"))
		status := strings.TrimSpace(r.FormValue("status"))

		if name == "" || targetAmountStr == "" {
			jsonError(w, "goal name and target amount are required", http.StatusBadRequest)
			return
		}

		targetAmount, err := strconv.ParseFloat(targetAmountStr, 64)
		if err != nil || targetAmount <= 0 {
			jsonError(w, "target amount must be greater than zero", http.StatusBadRequest)
			return
		}

		var targetDate *time.Time
		if targetDateStr != "" {
			if parsed, err := time.Parse("2006-01-02", targetDateStr); err == nil {
				targetDate = &parsed
			}
		}

		goal, err := app.DB.UpdateGoal(id, username, name, targetAmount, targetDate, emoji, color, category, status)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		goal.TargetFmt = formatMoney(goal.TargetAmount, curr)
		goal.SavedFmt = formatMoney(goal.SavedAmount, curr)
		goal.RemainingFmt = formatMoney(goal.Remaining, curr)
		jsonOK(w, goal)

	case http.MethodDelete:
		deleted, err := app.DB.DeleteGoal(id, username)
		if err != nil || !deleted {
			jsonError(w, "goal not found or delete failed", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Custom Categories & Tags Handlers ──────────────────────────────────────

func (app *App) HandleCategoriesAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cats, err := app.DB.GetCategories(username)
		if err != nil {
			jsonError(w, "failed to fetch categories", http.StatusInternalServerError)
			return
		}
		jsonOK(w, cats)

	case http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = strings.TrimSpace(r.FormValue("label"))
		}
		catType := strings.TrimSpace(r.FormValue("type"))
		emoji := strings.TrimSpace(r.FormValue("emoji"))
		if emoji == "" {
			emoji = strings.TrimSpace(r.FormValue("icon"))
		}
		color := strings.TrimSpace(r.FormValue("color"))

		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Name  string `json:"name"`
				Label string `json:"label"`
				Type  string `json:"type"`
				Emoji string `json:"emoji"`
				Icon  string `json:"icon"`
				Color string `json:"color"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if body.Name != "" {
					name = body.Name
				} else if body.Label != "" {
					name = body.Label
				}
				if body.Type != "" {
					catType = body.Type
				}
				if body.Emoji != "" {
					emoji = body.Emoji
				} else if body.Icon != "" {
					emoji = body.Icon
				}
				if body.Color != "" {
					color = body.Color
				}
			}
		}

		if name == "" {
			jsonError(w, "category name is required", http.StatusBadRequest)
			return
		}

		cat, err := app.DB.CreateCategory(username, name, catType, emoji, color)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, cat)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleCategoryByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/categories/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonError(w, "invalid category id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = strings.TrimSpace(r.FormValue("label"))
		}
		emoji := strings.TrimSpace(r.FormValue("emoji"))
		if emoji == "" {
			emoji = strings.TrimSpace(r.FormValue("icon"))
		}
		color := strings.TrimSpace(r.FormValue("color"))

		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Name  string `json:"name"`
				Label string `json:"label"`
				Emoji string `json:"emoji"`
				Icon  string `json:"icon"`
				Color string `json:"color"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if body.Name != "" {
					name = body.Name
				} else if body.Label != "" {
					name = body.Label
				}
				if body.Emoji != "" {
					emoji = body.Emoji
				} else if body.Icon != "" {
					emoji = body.Icon
				}
				if body.Color != "" {
					color = body.Color
				}
			}
		}

		if name == "" {
			jsonError(w, "category name is required", http.StatusBadRequest)
			return
		}

		cat, err := app.DB.UpdateCategory(id, username, name, emoji, color)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, cat)

	case http.MethodDelete:
		_ = r.ParseMultipartForm(1 << 20)
		reassignTo := strings.TrimSpace(r.FormValue("reassign_to"))
		if reassignTo == "" {
			reassignTo = strings.TrimSpace(r.URL.Query().Get("reassign_to"))
		}

		deleted, err := app.DB.DeleteCategory(id, username, reassignTo)
		if err != nil || !deleted {
			jsonError(w, "category not found or cannot be deleted", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleTagsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	tags, err := app.DB.GetPopularTags(username)
	if err != nil {
		jsonError(w, "failed to fetch tags", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"tags": tags})
}



