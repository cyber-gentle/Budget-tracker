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

	// Pages
	app.Mux.HandleFunc("/", app.HandleHome)
	app.Mux.HandleFunc("/login", app.HandleLogin)
	app.Mux.HandleFunc("/sign-up", app.HandleSignUp)
	app.Mux.HandleFunc("/dashboard", app.HandleDashboard)

	// Auth APIs
	app.Mux.HandleFunc("/api/signup", app.HandleSignupAPI)
	app.Mux.HandleFunc("/api/login", app.HandleLoginAPI)
	app.Mux.HandleFunc("/api/logout", app.HandleLogoutAPI)

	// Transaction APIs
	app.Mux.HandleFunc("/api/transactions", app.HandleTransactions)
	app.Mux.HandleFunc("/api/transactions/", app.HandleTransactionByID)

	// Budgets, Analytics, Export, Currency APIs
	app.Mux.HandleFunc("/api/budgets", app.HandleBudgets)
	app.Mux.HandleFunc("/api/analytics", app.HandleAnalytics)
	app.Mux.HandleFunc("/api/export", app.HandleExportCSV)
	app.Mux.HandleFunc("/api/currency", app.HandleCurrencyAPI)

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
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	app.renderTemplate(w, "index.html", nil)
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

	data := struct {
		Username    string
		Currency    string
		IncomeFmt   string
		ExpensesFmt string
		BalanceFmt  string
		SavingsRate int
	}{
		Username:    username,
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
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if err := app.DB.Signup(username, password); err != nil {
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
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	token, err := app.DB.Login(username, password)
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

		if err := app.DB.AddTransaction(username, amount, category, note, txnType, txDate); err != nil {
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

		updated, err := app.DB.UpdateTransaction(id, username, amount, category, note, txnType, optDate...)
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

	income, expense, balance, _ := app.DB.CalculateTotals(username)
	breakdown, _ := app.DB.CategoryBreakdown(username, false)
	trends, _ := app.DB.GetMonthlyTrends(username, 6)

	jsonOK(w, map[string]any{
		"category_breakdown": breakdown,
		"trends":             trends,
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
