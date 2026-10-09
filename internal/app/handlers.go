package app

import (
	"embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*
var embeddedFS embed.FS

// App holds the shared application state and routing.
type App struct {
	DB            *DBStore
	Tmpl          *template.Template
	Mux           *http.ServeMux
	loginLimiter  *IPRateLimiter
	signupLimiter *IPRateLimiter
}

// isAllowedOrigin validates if an origin is permitted for CORS.
func isAllowedOrigin(origin, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Allow browser extensions (e.g. Mobile Viewer simulator)
	if u.Scheme == "chrome-extension" || u.Scheme == "moz-extension" {
		return true
	}
	// Allow same host
	if u.Host == host {
		return true
	}
	hostname := u.Hostname()
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "0.0.0.0" {
		return true
	}
	if strings.HasSuffix(hostname, ".vercel.app") || hostname == "spendly.app" || strings.HasSuffix(hostname, ".spendly.app") {
		return true
	}
	return false
}

// setSessionCookie sets an HttpOnly session cookie, marking Secure when on HTTPS.
func (app *App) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 86400,
	})
}

// NewApp creates an App, parses templates, and registers all routes.
func NewApp(db *DBStore) *App {
	tmpl := template.Must(template.ParseFS(embeddedFS, "templates/html/*.html"))

	app := &App{
		DB:            db,
		Tmpl:          tmpl,
		Mux:           http.NewServeMux(),
		loginLimiter:  NewIPRateLimiter(20, time.Minute),    // max 20 login attempts per minute per IP
		signupLimiter: NewIPRateLimiter(10, 10*time.Minute), // max 10 signups per 10 minutes per IP
	}

	app.routes()
	return app
}

// ServeHTTP delegates to the internal mux with strict security headers and validated CORS origin.
func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" {
		if !isAllowedOrigin(origin, r.Host) {
			http.Error(w, "forbidden cross-origin request", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, session")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}

	// Security Defense Headers
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' chrome-extension: moz-extension:;")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	app.Mux.ServeHTTP(w, r)
}

func (app *App) routes() {
	// Static assets from embedded FS
	staticFS, err := fs.Sub(embeddedFS, "templates")
	if err != nil {
		log.Fatalf("failed to create static sub fs: %v", err)
	}
	app.Mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Uploaded receipts & documents storage
	_ = os.MkdirAll("uploads/receipts", 0755)
	app.Mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

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
	app.Mux.HandleFunc("/transactions", app.HandleTransactionsPage)
	app.Mux.HandleFunc("/debts", app.HandleDebts)
	app.Mux.HandleFunc("/subscriptions", app.HandleSubscriptionsPage)
	app.Mux.HandleFunc("/goals", app.HandleGoalsPage)
	app.Mux.HandleFunc("/reports", app.HandleReportsPage)
	app.Mux.HandleFunc("/net-worth", app.HandleNetWorthPage)
	app.Mux.HandleFunc("/receipts", app.HandleReceiptsPage)
	app.Mux.HandleFunc("/forecast", app.HandleForecastPage)

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

	// Split Expenses & Shared IOUs APIs
	app.Mux.HandleFunc("/api/splits", app.HandleSplitsAPI)
	app.Mux.HandleFunc("/api/splits/", app.HandleSplitByID)

	// Settlement Calculator & Mutual Netting APIs
	app.Mux.HandleFunc("/api/settlements", app.HandleSettlementOverviewAPI)
	app.Mux.HandleFunc("/api/settlements/settle", app.HandleSettleContactDebtsAPI)

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

	// Multi-Account & Wallet APIs
	app.Mux.HandleFunc("/api/accounts", app.HandleAccountsAPI)
	app.Mux.HandleFunc("/api/accounts/transfer", app.HandleAccountTransferAPI)
	app.Mux.HandleFunc("/api/accounts/transfers", app.HandleAccountTransfersListAPI)
	app.Mux.HandleFunc("/api/accounts/", app.HandleAccountByID)

	// Budgets, Analytics, Reports, Export, Currency APIs
	app.Mux.HandleFunc("/api/budgets", app.HandleBudgets)
	app.Mux.HandleFunc("/api/budgets/smart", app.HandleSmartBudgetsAPI)
	app.Mux.HandleFunc("/api/budgets/auto-503020", app.HandleAuto503020API)
	app.Mux.HandleFunc("/api/analytics", app.HandleAnalytics)
	app.Mux.HandleFunc("/api/reports", app.HandleReportsAPI)
	app.Mux.HandleFunc("/api/export", app.HandleExportCSV)
	app.Mux.HandleFunc("/api/currency", app.HandleCurrencyAPI)
	app.Mux.HandleFunc("/api/profile", app.HandleProfileAPI)

	// Net Worth & Wealth APIs
	app.Mux.HandleFunc("/api/net-worth", app.HandleNetWorthAPI)
	app.Mux.HandleFunc("/api/net-worth/items", app.HandleCustomAssetLiabilityAPI)
	app.Mux.HandleFunc("/api/net-worth/items/", app.HandleCustomAssetLiabilityByIDAPI)

	// Receipts & Document OCR Scanner APIs
	app.Mux.HandleFunc("/api/receipts", app.HandleReceiptsAPI)
	app.Mux.HandleFunc("/api/receipts/scan", app.HandleReceiptScanAPI)
	app.Mux.HandleFunc("/api/receipts/parse-text", app.HandleParseReceiptTextAPI)
	app.Mux.HandleFunc("/api/receipts/", app.HandleReceiptByIDAPI)

	// Cash Flow Forecasting & Runway Predictor APIs
	app.Mux.HandleFunc("/api/forecast", app.HandleForecastAPI)
	app.Mux.HandleFunc("/api/forecast/simulate", app.HandleForecastSimulateAPI)
	app.Mux.HandleFunc("/api/forecast/settings", app.HandleForecastSettingsAPI)
	app.Mux.HandleFunc("/api/forecast/incomes", app.HandleForecastIncomesAPI)
	app.Mux.HandleFunc("/api/forecast/incomes/", app.HandleForecastIncomeByIDAPI)

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

// getSessionUser extracts the username from the session cookie, query param, or header.
func (app *App) getSessionUser(r *http.Request) (string, bool) {
	token := ""
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		token = c.Value
	} else if q := r.URL.Query().Get("session"); q != "" {
		token = q
	} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}
	if token == "" {
		return "", false
	}
	return app.DB.ValidateSession(token)
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
	firstName := username
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
		if strings.TrimSpace(prof.FullName) != "" {
			parts := strings.Fields(prof.FullName)
			if len(parts) > 0 {
				firstName = parts[0]
			}
		}
	}
	dateGreeting := time.Now().Format("Monday, Jan 2")

	data := struct {
		Username     string
		FirstName    string
		DateGreeting string
		FullName     string
		Email        string
		Currency     string
		IncomeFmt    string
		ExpensesFmt  string
		BalanceFmt   string
		SavingsRate  int
	}{
		Username:     username,
		FirstName:    firstName,
		DateGreeting: dateGreeting,
		FullName:     fullName,
		Email:        email,
		Currency:     curr,
		IncomeFmt:    formatMoney(income, curr),
		ExpensesFmt:  formatMoney(expenses, curr),
		BalanceFmt:   formatMoney(balance, curr),
		SavingsRate:  savingsRate,
	}

	app.renderTemplate(w, "dashboard.html", data)
}

func (app *App) HandleTransactionsPage(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := app.DB.GetCurrency(username)
	income, expenses, balance, _ := app.DB.CalculateTotals(username)

	prof, _ := app.DB.GetProfile(username)
	fullName := ""
	email := ""
	firstName := username
	if prof != nil {
		fullName = prof.FullName
		email = prof.Email
		if strings.TrimSpace(prof.FullName) != "" {
			parts := strings.Fields(prof.FullName)
			if len(parts) > 0 {
				firstName = parts[0]
			}
		}
	}

	data := struct {
		Username    string
		FirstName   string
		FullName    string
		Email       string
		Currency    string
		IncomeFmt   string
		ExpensesFmt string
		BalanceFmt  string
	}{
		Username:    username,
		FirstName:   firstName,
		FullName:    fullName,
		Email:       email,
		Currency:    curr,
		IncomeFmt:   formatMoney(income, curr),
		ExpensesFmt: formatMoney(expenses, curr),
		BalanceFmt:  formatMoney(balance, curr),
	}

	app.renderTemplate(w, "transactions.html", data)
}

// ─── Auth API Handlers ─────────────────────────────────────────────────────

func (app *App) HandleSignupAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting: prevent automated account spam
	if !app.signupLimiter.Allow(GetClientIP(r)) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			jsonError(w, "too many signup attempts, please try again in a few minutes", http.StatusTooManyRequests)
			return
		}
		http.Redirect(w, r, "/sign-up?error=Too+many+attempts.+Please+wait+a+few+minutes.", http.StatusSeeOther)
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
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		http.Redirect(w, r, "/sign-up?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	token, err := app.DB.Login(username, password)
	if err != nil {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/login?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	app.setSessionCookie(w, r, token)
	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		jsonOK(w, map[string]string{
			"redirect": "/dashboard",
			"token":    token,
		})
		return
	}
	http.Redirect(w, r, "/dashboard?session="+token, http.StatusSeeOther)
}

func (app *App) HandleLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limiting: prevent credential brute-forcing
	if !app.loginLimiter.Allow(GetClientIP(r)) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			jsonError(w, "too many login attempts, please try again in a minute", http.StatusTooManyRequests)
			return
		}
		http.Redirect(w, r, "/login?error=Too+many+attempts.+Please+wait+a+moment.", http.StatusSeeOther)
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
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			jsonError(w, err.Error(), http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	app.setSessionCookie(w, r, token)
	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		jsonOK(w, map[string]string{
			"redirect": "/dashboard",
			"token":    token,
		})
		return
	}
	http.Redirect(w, r, "/dashboard?session="+token, http.StatusSeeOther)
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
		accountID, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("account_id")))
		if err := app.DB.AddTransactionFull(username, amount, category, note, txnType, tags, accountID, txDate); err != nil {
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
		accountID, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("account_id")))
		updated, err := app.DB.UpdateTransactionFull(id, username, amount, category, note, txnType, tags, accountID, optDate...)
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
		smartRep, _ := app.DB.GetSmartBudgetReport(username)
		jsonOK(w, map[string]any{
			"budgets":      budgets,
			"spending":     spending,
			"currency":     app.DB.GetCurrency(username),
			"smart_report": smartRep,
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
		smartRep, _ := app.DB.GetSmartBudgetReport(username)
		jsonOK(w, map[string]any{
			"status":       "ok",
			"budgets":      budgets,
			"spending":     spending,
			"smart_report": smartRep,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleSmartBudgetsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	report, err := app.DB.GetSmartBudgetReport(username)
	if err != nil {
		jsonError(w, "failed to compute smart budget report: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, report)
}

func (app *App) HandleAuto503020API(w http.ResponseWriter, r *http.Request) {
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
	var baseIncome float64
	incomeStr := strings.TrimSpace(r.FormValue("base_income"))
	if incomeStr != "" {
		baseIncome, _ = strconv.ParseFloat(incomeStr, 64)
	}

	budgets, err := app.DB.Apply503020AutoBudget(username, baseIncome)
	if err != nil {
		jsonError(w, "failed to apply auto 50/30/20 budget: "+err.Error(), http.StatusInternalServerError)
		return
	}

	report, _ := app.DB.GetSmartBudgetReport(username)
	jsonOK(w, map[string]any{
		"status":  "ok",
		"message": "50/30/20 budget allocations successfully configured!",
		"budgets": budgets,
		"report":  report,
	})
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

	// Mobile monthly overview calculations
	thisMonthSavings := thisMonthIncome - thisMonthExpense
	day := now.Day()
	if day < 1 {
		day = 1
	}
	dailyAvg := thisMonthExpense / float64(day)

	// Previous month calculations for Month-over-Month deltas
	prevMonth := now.AddDate(0, -1, 0)
	var prevMonthIncome, prevMonthExpense float64
	for _, t := range txs {
		if t.Date.Year() == prevMonth.Year() && t.Date.Month() == prevMonth.Month() {
			if t.Type == "income" {
				prevMonthIncome += t.Amount
			} else {
				prevMonthExpense += t.Amount
			}
		}
	}
	prevMonthSavings := prevMonthIncome - prevMonthExpense
	daysInPrevMonth := time.Date(prevMonth.Year(), prevMonth.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	prevDailyAvg := 0.0
	if daysInPrevMonth > 0 {
		prevDailyAvg = prevMonthExpense / float64(daysInPrevMonth)
	}

	savingsMomPct := 0.0
	savingsMomDir := "up"
	if prevMonthSavings != 0 {
		diff := ((thisMonthSavings - prevMonthSavings) / math.Abs(prevMonthSavings)) * 100
		if diff >= 0 {
			savingsMomDir = "up"
			savingsMomPct = diff
		} else {
			savingsMomDir = "down"
			savingsMomPct = math.Abs(diff)
		}
	} else if thisMonthSavings > 0 {
		savingsMomDir = "up"
		savingsMomPct = 100
	}

	dailyAvgMomPct := 0.0
	dailyAvgMomDir := "down"
	if prevDailyAvg != 0 {
		diff := ((dailyAvg - prevDailyAvg) / prevDailyAvg) * 100
		if diff >= 0 {
			dailyAvgMomDir = "up"
			dailyAvgMomPct = diff
		} else {
			dailyAvgMomDir = "down"
			dailyAvgMomPct = math.Abs(diff)
		}
	}

	budgets, _ := app.DB.GetBudgets(username)
	var totalBudgetLimit float64
	for _, limit := range budgets {
		totalBudgetLimit += limit
	}
	budgetUsedPct := 0
	if totalBudgetLimit > 0 {
		budgetUsedPct = int((thisMonthExpense / totalBudgetLimit) * 100)
		if budgetUsedPct > 100 {
			budgetUsedPct = 100
		}
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
			"this_month_savings": thisMonthSavings,
			"daily_average":      dailyAvg,
			"savings_mom_pct":    savingsMomPct,
			"savings_mom_dir":    savingsMomDir,
			"daily_avg_mom_pct":  dailyAvgMomPct,
			"daily_avg_mom_dir":  dailyAvgMomDir,
			"prev_month_name":    prevMonth.Format("Jan"),
			"budget_used_pct":    budgetUsedPct,
			"savings_rate":       savingsRate,
			"currency":           app.DB.GetCurrency(username),
		},
	})
}

// ─── Financial Reports & Analytics Handlers ─────────────────────────────────

func (app *App) HandleReportsPage(w http.ResponseWriter, r *http.Request) {
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

	app.renderTemplate(w, "reports.html", data)
}

func (app *App) HandleReportsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	timeframe := r.URL.Query().Get("timeframe")
	startDateStr := r.URL.Query().Get("start")
	endDateStr := r.URL.Query().Get("end")
	accIDStr := r.URL.Query().Get("account_id")
	categoryFilter := r.URL.Query().Get("category")

	accID := 0
	if accIDStr != "" && accIDStr != "all" {
		accID, _ = strconv.Atoi(accIDStr)
	}

	report, err := app.DB.GetDetailedFinancialReport(username, timeframe, startDateStr, endDateStr, accID, categoryFilter)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, report)
}

// ─── CSV & JSON Export API Handler ──────────────────────────────────────────

func (app *App) HandleExportCSV(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	timeframe := r.URL.Query().Get("timeframe")
	startDateStr := r.URL.Query().Get("start")
	endDateStr := r.URL.Query().Get("end")
	accIDStr := r.URL.Query().Get("account_id")
	categoryFilter := r.URL.Query().Get("category")
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))

	accID := 0
	if accIDStr != "" && accIDStr != "all" {
		accID, _ = strconv.Atoi(accIDStr)
	}

	report, err := app.DB.GetDetailedFinancialReport(username, timeframe, startDateStr, endDateStr, accID, categoryFilter)
	if err != nil {
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}

	txs := report.Transactions
	if typeFilter != "" && typeFilter != "all" {
		var matched []Transaction
		for _, t := range txs {
			if strings.ToLower(t.Type) == typeFilter {
				matched = append(matched, t)
			}
		}
		txs = matched
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"spendly_%s_export.json\"", username))
		_ = json.NewEncoder(w).Encode(txs)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"spendly_%s_transactions.csv\"", username))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	writer.Write([]string{"ID", "Date", "Type", "Category", "Wallet", "Amount", "Tags", "Note"})
	for _, t := range txs {
		accName := t.AccountName
		if accName == "" {
			accName = "Main Wallet"
		}
		writer.Write([]string{
			strconv.Itoa(t.ID),
			t.Date.Format("2006-01-02 15:04:05"),
			t.Type,
			t.Category,
			accName,
			fmt.Sprintf("%.2f", t.Amount),
			t.Tags,
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

// ─── Multi-Account & Wallet Handlers ─────────────────────────────────────────

func (app *App) HandleAccountsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		accounts, err := app.DB.GetAccounts(username)
		if err != nil {
			jsonError(w, "failed to fetch accounts", http.StatusInternalServerError)
			return
		}
		curr := app.DB.GetCurrency(username)
		totalBalance := 0.0
		for _, a := range accounts {
			if a.Type == "credit" {
				totalBalance -= a.CurrentBalance
			} else {
				totalBalance += a.CurrentBalance
			}
		}
		jsonOK(w, map[string]any{
			"accounts":      accounts,
			"total_balance": totalBalance,
			"total_fmt":     formatMoney(totalBalance, curr),
			"currency":      curr,
		})

	case http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		accType := strings.TrimSpace(r.FormValue("type"))
		currency := strings.TrimSpace(r.FormValue("currency"))
		initBalStr := strings.TrimSpace(r.FormValue("initial_balance"))
		color := strings.TrimSpace(r.FormValue("color"))
		icon := strings.TrimSpace(r.FormValue("icon"))
		if icon == "" {
			icon = strings.TrimSpace(r.FormValue("emoji"))
		}
		isDefaultStr := strings.TrimSpace(r.FormValue("is_default"))
		isDefault := isDefaultStr == "1" || strings.ToLower(isDefaultStr) == "true"

		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Name           string  `json:"name"`
				Type           string  `json:"type"`
				Currency       string  `json:"currency"`
				InitialBalance float64 `json:"initial_balance"`
				Color          string  `json:"color"`
				Icon           string  `json:"icon"`
				Emoji          string  `json:"emoji"`
				IsDefault      bool    `json:"is_default"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if body.Name != "" {
					name = body.Name
				}
				if body.Type != "" {
					accType = body.Type
				}
				if body.Currency != "" {
					currency = body.Currency
				}
				if body.InitialBalance != 0 {
					initBalStr = fmt.Sprintf("%f", body.InitialBalance)
				}
				if body.Color != "" {
					color = body.Color
				}
				if body.Icon != "" {
					icon = body.Icon
				} else if body.Emoji != "" {
					icon = body.Emoji
				}
				if body.IsDefault {
					isDefault = true
				}
			}
		}

		if name == "" {
			jsonError(w, "account name is required", http.StatusBadRequest)
			return
		}

		initialBalance := 0.0
		if initBalStr != "" {
			if parsed, err := strconv.ParseFloat(initBalStr, 64); err == nil {
				initialBalance = parsed
			}
		}

		acc, err := app.DB.CreateAccount(username, name, accType, currency, color, icon, initialBalance, isDefault)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, acc)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleAccountByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	if parts[0] == "transfer" {
		app.HandleAccountTransferAPI(w, r)
		return
	}
	if parts[0] == "transfers" {
		app.HandleAccountTransfersListAPI(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil || id <= 0 {
		jsonError(w, "invalid account id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		acc, err := app.DB.GetAccountByID(id, username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonOK(w, acc)

	case http.MethodPut, http.MethodPost:
		_ = r.ParseMultipartForm(1 << 20)
		name := strings.TrimSpace(r.FormValue("name"))
		accType := strings.TrimSpace(r.FormValue("type"))
		currency := strings.TrimSpace(r.FormValue("currency"))
		initBalStr := strings.TrimSpace(r.FormValue("initial_balance"))
		color := strings.TrimSpace(r.FormValue("color"))
		icon := strings.TrimSpace(r.FormValue("icon"))
		if icon == "" {
			icon = strings.TrimSpace(r.FormValue("emoji"))
		}
		isDefaultStr := strings.TrimSpace(r.FormValue("is_default"))
		isDefault := isDefaultStr == "1" || strings.ToLower(isDefaultStr) == "true"

		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Name           string  `json:"name"`
				Type           string  `json:"type"`
				Currency       string  `json:"currency"`
				InitialBalance float64 `json:"initial_balance"`
				Color          string  `json:"color"`
				Icon           string  `json:"icon"`
				Emoji          string  `json:"emoji"`
				IsDefault      bool    `json:"is_default"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if body.Name != "" {
					name = body.Name
				}
				if body.Type != "" {
					accType = body.Type
				}
				if body.Currency != "" {
					currency = body.Currency
				}
				if body.InitialBalance != 0 {
					initBalStr = fmt.Sprintf("%f", body.InitialBalance)
				}
				if body.Color != "" {
					color = body.Color
				}
				if body.Icon != "" {
					icon = body.Icon
				} else if body.Emoji != "" {
					icon = body.Emoji
				}
				if body.IsDefault {
					isDefault = true
				}
			}
		}

		if name == "" {
			jsonError(w, "account name is required", http.StatusBadRequest)
			return
		}

		initialBalance := 0.0
		if initBalStr != "" {
			if parsed, err := strconv.ParseFloat(initBalStr, 64); err == nil {
				initialBalance = parsed
			}
		}

		acc, err := app.DB.UpdateAccount(id, username, name, accType, currency, color, icon, initialBalance, isDefault)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, acc)

	case http.MethodDelete:
		_ = r.ParseMultipartForm(1 << 20)
		reassignStr := strings.TrimSpace(r.FormValue("reassign_to"))
		if reassignStr == "" {
			reassignStr = strings.TrimSpace(r.URL.Query().Get("reassign_to"))
		}
		reassignID, _ := strconv.Atoi(reassignStr)

		deleted, err := app.DB.DeleteAccount(id, username, reassignID)
		if err != nil || !deleted {
			msg := "account not found or cannot be deleted"
			if err != nil {
				msg = err.Error()
			}
			jsonError(w, msg, http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleAccountTransferAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_ = r.ParseMultipartForm(1 << 20)
	fromID, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("from_account_id")))
	toID, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("to_account_id")))
	amountStr := strings.TrimSpace(r.FormValue("amount"))
	note := strings.TrimSpace(r.FormValue("note"))
	dateStr := strings.TrimSpace(r.FormValue("date"))

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			FromAccountID int     `json:"from_account_id"`
			ToAccountID   int     `json:"to_account_id"`
			Amount        float64 `json:"amount"`
			Note          string  `json:"note"`
			Date          string  `json:"date"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if body.FromAccountID > 0 {
				fromID = body.FromAccountID
			}
			if body.ToAccountID > 0 {
				toID = body.ToAccountID
			}
			if body.Amount > 0 {
				amountStr = fmt.Sprintf("%f", body.Amount)
			}
			if body.Note != "" {
				note = body.Note
			}
			if body.Date != "" {
				dateStr = body.Date
			}
		}
	}

	if fromID <= 0 || toID <= 0 {
		jsonError(w, "source and destination accounts are required", http.StatusBadRequest)
		return
	}
	if fromID == toID {
		jsonError(w, "source and destination accounts cannot be the same", http.StatusBadRequest)
		return
	}

	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 {
		jsonError(w, "transfer amount must be greater than zero", http.StatusBadRequest)
		return
	}

	txDate := time.Now()
	if dateStr != "" {
		if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
			now := time.Now()
			txDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local)
		}
	}

	tr, err := app.DB.CreateAccountTransfer(username, fromID, toID, amount, note, txDate)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, tr)
}

func (app *App) HandleAccountTransfersListAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	transfers, err := app.DB.GetAccountTransfers(username, 50)
	if err != nil {
		jsonError(w, "failed to fetch transfers", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"transfers": transfers})
}

// ─── Split Expenses & Shared IOUs Handlers ─────────────────────────────────

func (app *App) HandleSplitsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		splits, err := app.DB.GetSplits(username)
		if err != nil {
			jsonError(w, "failed to get split expenses", http.StatusInternalServerError)
			return
		}
		if splits == nil {
			splits = []SplitExpense{}
		}
		jsonOK(w, map[string]any{"splits": splits})

	case http.MethodPost:
		type SplitReq struct {
			Title          string                  `json:"title"`
			TotalAmount    float64                 `json:"total_amount"`
			PayerName      string                  `json:"payer_name"`
			PayerIsUser    bool                    `json:"payer_is_user"`
			Category       string                  `json:"category"`
			Date           string                  `json:"date"`
			SplitType      string                  `json:"split_type"`
			Note           string                  `json:"note"`
			LogTransaction bool                    `json:"log_transaction"`
			AccountID      int                     `json:"account_id"`
			Participants   []SplitParticipantInput `json:"participants"`
		}

		var req SplitReq
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				jsonError(w, "invalid JSON payload: "+err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			_ = r.ParseMultipartForm(1 << 20)
			req.Title = r.FormValue("title")
			req.TotalAmount, _ = strconv.ParseFloat(r.FormValue("total_amount"), 64)
			req.PayerName = r.FormValue("payer_name")
			req.PayerIsUser = (r.FormValue("payer_is_user") == "1" || r.FormValue("payer_is_user") == "true")
			req.Category = r.FormValue("category")
			req.Date = r.FormValue("date")
			req.SplitType = r.FormValue("split_type")
			req.Note = r.FormValue("note")
			req.LogTransaction = (r.FormValue("log_transaction") == "1" || r.FormValue("log_transaction") == "true")
			req.AccountID, _ = strconv.Atoi(r.FormValue("account_id"))
			partsJSON := r.FormValue("participants")
			if partsJSON != "" {
				_ = json.Unmarshal([]byte(partsJSON), &req.Participants)
			}
		}

		if req.Title == "" || req.TotalAmount <= 0 {
			jsonError(w, "title and total amount must be specified", http.StatusBadRequest)
			return
		}
		if len(req.Participants) < 2 {
			jsonError(w, "at least 2 participants are required to split an expense", http.StatusBadRequest)
			return
		}

		var dt time.Time
		if req.Date != "" {
			if parsed, err := time.Parse("2006-01-02", req.Date); err == nil {
				dt = parsed
			}
		}
		if dt.IsZero() {
			dt = time.Now()
		}

		split, err := app.DB.CreateSplitExpense(
			username, req.Title, req.Category, req.Note,
			req.TotalAmount, dt, req.PayerName, req.PayerIsUser,
			req.SplitType, req.Participants, req.LogTransaction, req.AccountID,
		)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, split)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleSplitByID(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/splits/")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		jsonError(w, "invalid split ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		split, err := app.DB.GetSplitByID(id, username)
		if err != nil {
			jsonError(w, "split expense not found", http.StatusNotFound)
			return
		}
		jsonOK(w, split)

	case http.MethodDelete:
		deleted, err := app.DB.DeleteSplit(id, username)
		if err != nil || !deleted {
			jsonError(w, "failed to delete split expense", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"deleted": true, "id": id})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleSettlementOverviewAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	overview, err := app.DB.GetSettlementOverview(username)
	if err != nil {
		jsonError(w, "failed to calculate settlements: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, overview)
}

func (app *App) HandleSettleContactDebtsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type SettleReq struct {
		ContactName    string `json:"contact_name"`
		Mode           string `json:"mode"`
		AccountID      int    `json:"account_id"`
		LogTransaction bool   `json:"log_transaction"`
	}

	var req SettleReq
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
	} else {
		_ = r.ParseMultipartForm(1 << 20)
		req.ContactName = r.FormValue("contact_name")
		req.Mode = r.FormValue("mode")
		req.AccountID, _ = strconv.Atoi(r.FormValue("account_id"))
		req.LogTransaction = (r.FormValue("log_transaction") == "1" || r.FormValue("log_transaction") == "true")
	}

	if req.ContactName == "" {
		jsonError(w, "contact name is required", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = "full"
	}

	res, err := app.DB.SettleContactDebts(username, req.ContactName, req.Mode, req.AccountID, req.LogTransaction)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, res)
}

// ─── Net Worth & Asset / Liability Handlers ──────────────────────────────────

func (app *App) HandleNetWorthPage(w http.ResponseWriter, r *http.Request) {
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

	overview, err := app.DB.GetNetWorthOverview(username)
	if err != nil {
		overview = &NetWorthOverview{
			Currency:        curr,
			AssetsList:      []NetWorthItem{},
			LiabilitiesList: []NetWorthItem{},
			CustomItems:     []CustomAssetLiability{},
			Trend:           []NetWorthTrendPoint{},
		}
	}

	data := struct {
		Username string
		FullName string
		Email    string
		Currency string
		Overview *NetWorthOverview
	}{
		Username: username,
		FullName: fullName,
		Email:    email,
		Currency: curr,
		Overview: overview,
	}

	app.renderTemplate(w, "net-worth.html", data)
}

func (app *App) HandleNetWorthAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	overview, err := app.DB.GetNetWorthOverview(username)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, overview)
}

func (app *App) HandleCustomAssetLiabilityAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := app.DB.GetCustomAssetLiabilities(username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"items": items})

	case http.MethodPost:
		var item CustomAssetLiability
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
				jsonError(w, "invalid JSON payload", http.StatusBadRequest)
				return
			}
		} else {
			_ = r.ParseMultipartForm(1 << 20)
			item.Name = r.FormValue("name")
			item.Type = r.FormValue("type")
			item.Category = r.FormValue("category")
			item.Institution = r.FormValue("institution")
			item.Notes = r.FormValue("notes")
			item.Amount, _ = strconv.ParseFloat(r.FormValue("amount"), 64)
		}

		if strings.TrimSpace(item.Name) == "" {
			jsonError(w, "name is required", http.StatusBadRequest)
			return
		}
		if item.Amount < 0 {
			jsonError(w, "amount cannot be negative", http.StatusBadRequest)
			return
		}

		saved, err := app.DB.AddCustomAssetLiability(username, item)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "item": saved})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleCustomAssetLiabilityByIDAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		jsonError(w, "invalid ID in path", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(pathParts[3])
	if err != nil || id <= 0 {
		jsonError(w, "invalid item ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		var item CustomAssetLiability
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
				jsonError(w, "invalid JSON payload", http.StatusBadRequest)
				return
			}
		} else {
			_ = r.ParseMultipartForm(1 << 20)
			item.Name = r.FormValue("name")
			item.Type = r.FormValue("type")
			item.Category = r.FormValue("category")
			item.Institution = r.FormValue("institution")
			item.Notes = r.FormValue("notes")
			item.Amount, _ = strconv.ParseFloat(r.FormValue("amount"), 64)
		}

		saved, err := app.DB.UpdateCustomAssetLiability(username, id, item)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		jsonOK(w, map[string]any{"success": true, "item": saved})

	case http.MethodDelete:
		ok, err := app.DB.DeleteCustomAssetLiability(username, id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			jsonError(w, "item not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]any{"success": true})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Receipt & Document OCR Scanner Handlers ────────────────────────────────

func (app *App) HandleReceiptsPage(w http.ResponseWriter, r *http.Request) {
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

	receipts, summary, err := app.DB.GetReceipts(username)
	if err != nil {
		receipts = []Receipt{}
	}

	accounts, _ := app.DB.GetAccounts(username)
	cats, _ := app.DB.GetCategories(username)

	data := map[string]any{
		"Username":   username,
		"FullName":   fullName,
		"Email":      email,
		"Currency":   curr,
		"Receipts":   receipts,
		"Summary":    summary,
		"Accounts":   accounts,
		"Categories": cats,
	}

	app.renderTemplate(w, "receipts.html", data)
}

func (app *App) HandleReceiptsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		receipts, summary, err := app.DB.GetReceipts(username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{
			"receipts": receipts,
			"summary":  summary,
		})
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleReceiptScanAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var originalFilename string
	var savedFilePath string
	var rawOCRText string
	var manualMerchant string
	var manualAmount float64
	var manualCategory string

	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "multipart/form-data") {
		// Limit to 10MB
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			jsonError(w, "file too large or invalid multipart", http.StatusBadRequest)
			return
		}

		rawOCRText = r.FormValue("raw_ocr_text")
		manualMerchant = r.FormValue("merchant")
		manualCategory = r.FormValue("category")
		if amtStr := r.FormValue("amount"); amtStr != "" {
			manualAmount, _ = strconv.ParseFloat(amtStr, 64)
		}

		file, header, err := r.FormFile("file")
		if err == nil && file != nil {
			defer file.Close()
			originalFilename = filepath.Base(header.Filename)
			ext := strings.ToLower(filepath.Ext(originalFilename))
			allowedExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".pdf": true}
			if !allowedExts[ext] {
				ext = ".jpg"
			}
			cleanUser := strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
					return r
				}
				return -1
			}, username)
			if cleanUser == "" {
				cleanUser = "user"
			}
			safeName := fmt.Sprintf("receipt_%s_%d%s", cleanUser, time.Now().UnixNano(), ext)
			diskPath := filepath.Join("uploads", "receipts", safeName)

			out, err := os.Create(diskPath)
			if err != nil {
				jsonError(w, "failed to save receipt file", http.StatusInternalServerError)
				return
			}
			defer out.Close()
			if _, err := io.Copy(out, file); err != nil {
				jsonError(w, "failed to write receipt file", http.StatusInternalServerError)
				return
			}
			savedFilePath = "/uploads/receipts/" + safeName
		}
	} else {
		// JSON payload
		var req struct {
			ImageBase64 string  `json:"image_base64"`
			Filename    string  `json:"filename"`
			RawOCRText  string  `json:"raw_ocr_text"`
			Merchant    string  `json:"merchant"`
			Amount      float64 `json:"amount"`
			Category    string  `json:"category"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}

		rawOCRText = req.RawOCRText
		manualMerchant = req.Merchant
		manualAmount = req.Amount
		manualCategory = req.Category
		originalFilename = filepath.Base(req.Filename)
		if originalFilename == "" || originalFilename == "." {
			originalFilename = "scanned_receipt.jpg"
		}

		if req.ImageBase64 != "" {
			b64Data := req.ImageBase64
			ext := ".jpg"
			if idx := strings.Index(b64Data, ","); idx != -1 {
				header := b64Data[:idx]
				b64Data = b64Data[idx+1:]
				if strings.Contains(header, "png") {
					ext = ".png"
				} else if strings.Contains(header, "webp") {
					ext = ".webp"
				}
			}

			decoded, err := base64.StdEncoding.DecodeString(b64Data)
			if err == nil && len(decoded) > 0 && len(decoded) <= 15<<20 { // Max 15MB
				cleanUser := strings.Map(func(r rune) rune {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
						return r
					}
					return -1
				}, username)
				if cleanUser == "" {
					cleanUser = "user"
				}
				safeName := fmt.Sprintf("receipt_%s_%d%s", cleanUser, time.Now().UnixNano(), ext)
				diskPath := filepath.Join("uploads", "receipts", safeName)
				if err := os.WriteFile(diskPath, decoded, 0644); err == nil {
					savedFilePath = "/uploads/receipts/" + safeName
				}
			}
		}
	}

	if savedFilePath == "" {
		savedFilePath = "/static/assets/imgs/sample_receipt.png"
		if originalFilename == "" {
			originalFilename = "receipt.jpg"
		}
	}

	// Parse OCR text
	parsed := ParseReceiptText(rawOCRText)

	// Apply manual overrides if present
	merchant := parsed.Merchant
	if manualMerchant != "" {
		merchant = manualMerchant
	}
	totalAmount := parsed.TotalAmount
	if manualAmount > 0 {
		totalAmount = manualAmount
	}
	category := parsed.SuggestedCategory
	if manualCategory != "" {
		category = manualCategory
	}

	// Save to DB
	receipt, err := app.DB.CreateReceipt(
		username,
		savedFilePath,
		originalFilename,
		merchant,
		totalAmount,
		parsed.TaxAmount,
		parsed.TipAmount,
		parsed.ReceiptDate,
		category,
		rawOCRText,
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"receipt": receipt,
		"parsed":  parsed,
	})
}

func (app *App) HandleParseReceiptTextAPI(w http.ResponseWriter, r *http.Request) {
	_, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	parsed := ParseReceiptText(req.Text)
	jsonOK(w, map[string]any{
		"success": true,
		"parsed":  parsed,
	})
}

func (app *App) HandleReceiptByIDAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /api/receipts/{id} or /api/receipts/{id}/convert
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		jsonError(w, "invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.Atoi(pathParts[2])
	if err != nil || id <= 0 {
		jsonError(w, "invalid receipt ID", http.StatusBadRequest)
		return
	}

	isConvert := len(pathParts) >= 4 && pathParts[3] == "convert"

	if isConvert && r.Method == http.MethodPost {
		var req struct {
			Amount    float64 `json:"amount"`
			Category  string  `json:"category"`
			Note      string  `json:"note"`
			TxnType   string  `json:"txn_type"`
			AccountID int     `json:"account_id"`
			Tags      string  `json:"tags"`
			Date      string  `json:"date"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		receipt, err := app.DB.GetReceiptByID(id, username)
		if err != nil {
			jsonError(w, "receipt not found", http.StatusNotFound)
			return
		}

		amount := req.Amount
		if amount <= 0 {
			amount = receipt.TotalAmount
		}
		if amount <= 0 {
			jsonError(w, "amount must be greater than zero", http.StatusBadRequest)
			return
		}

		category := req.Category
		if category == "" {
			category = receipt.SuggestedCategory
		}
		if category == "" {
			category = "food"
		}

		note := req.Note
		if note == "" {
			note = receipt.Merchant
		}

		txnType := req.TxnType
		if txnType != "income" && txnType != "expense" {
			txnType = "expense"
		}

		tags := req.Tags
		if tags == "" {
			tags = "#receipt"
		} else if !strings.Contains(tags, "#receipt") {
			tags += ", #receipt"
		}

		var txDate time.Time
		if req.Date != "" {
			if parsed, err := time.Parse("2006-01-02", req.Date); err == nil {
				txDate = parsed
			}
		}
		if txDate.IsZero() && receipt.ReceiptDate != nil {
			txDate = *receipt.ReceiptDate
		}
		if txDate.IsZero() {
			txDate = time.Now()
		}

		txID, err := app.DB.AddTransactionWithReceipt(
			username,
			amount,
			category,
			note,
			txnType,
			tags,
			req.AccountID,
			receipt.FilePath,
			txDate,
		)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = app.DB.LinkReceiptToTransaction(id, txID, username)

		jsonOK(w, map[string]any{
			"success":        true,
			"transaction_id": txID,
			"receipt_id":     id,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		receipt, err := app.DB.GetReceiptByID(id, username)
		if err != nil {
			jsonError(w, "receipt not found", http.StatusNotFound)
			return
		}
		jsonOK(w, receipt)

	case http.MethodDelete:
		if err := app.DB.DeleteReceipt(id, username); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"success": true})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Step 11: Cash Flow Forecasting & Runway Predictor Handlers ─────────────────

func (app *App) HandleForecastPage(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	curr := "₦"
	fullName := ""
	email := ""
	if prof, err := app.DB.GetProfile(username); err == nil && prof != nil {
		if prof.Currency != "" {
			curr = prof.Currency
		}
		fullName = prof.FullName
		email = prof.Email
	}

	forecast, err := app.DB.GenerateCashFlowForecast(username, WhatIfSimulationParams{TimeframeDays: 30})
	if err != nil {
		log.Printf("error generating forecast: %v", err)
	}

	incomes, _ := app.DB.GetRecurringIncomes(username)
	subs, _ := app.DB.GetSubscriptions(username)
	accounts, _ := app.DB.GetAccounts(username)
	settings, _ := app.DB.GetCashFlowSettings(username)

	data := map[string]any{
		"Username":      username,
		"FullName":      fullName,
		"Email":         email,
		"Currency":      curr,
		"Forecast":      forecast,
		"Incomes":       incomes,
		"Subscriptions": subs,
		"Accounts":      accounts,
		"Settings":      settings,
	}

	app.renderTemplate(w, "forecast.html", data)
}

func (app *App) HandleForecastAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	timeframe := 30
	if tf, err := strconv.Atoi(q.Get("timeframe")); err == nil && (tf == 30 || tf == 60 || tf == 90) {
		timeframe = tf
	}

	discretionaryPct := 0.0
	if pct, err := strconv.ParseFloat(q.Get("discretionary_pct"), 64); err == nil {
		discretionaryPct = pct
	}

	incomeChange := 0.0
	if inc, err := strconv.ParseFloat(q.Get("income_change"), 64); err == nil {
		incomeChange = inc
	}

	plannedAmt := 0.0
	if pa, err := strconv.ParseFloat(q.Get("planned_expense_amount"), 64); err == nil {
		plannedAmt = pa
	}
	plannedDate := q.Get("planned_expense_date")
	plannedTitle := q.Get("planned_expense_title")

	safetyOverride := 0.0
	if so, err := strconv.ParseFloat(q.Get("safety_buffer"), 64); err == nil {
		safetyOverride = so
	}

	var excludedSubs []int
	if ex := q.Get("excluded_subs"); ex != "" {
		for _, part := range strings.Split(ex, ",") {
			if id, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
				excludedSubs = append(excludedSubs, id)
			}
		}
	}

	params := WhatIfSimulationParams{
		TimeframeDays:         timeframe,
		DiscretionarySpendPct: discretionaryPct,
		IncomeChangeMonthly:   incomeChange,
		PlannedExpenseAmount:  plannedAmt,
		PlannedExpenseDate:    plannedDate,
		PlannedExpenseTitle:   plannedTitle,
		SafetyBufferOverride:  safetyOverride,
		ExcludedSubIDs:        excludedSubs,
	}

	forecast, err := app.DB.GenerateCashFlowForecast(username, params)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, forecast)
}

func (app *App) HandleForecastSimulateAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var params WhatIfSimulationParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	forecast, err := app.DB.GenerateCashFlowForecast(username, params)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, forecast)
}

func (app *App) HandleForecastSettingsAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		settings, err := app.DB.GetCashFlowSettings(username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, settings)

	case http.MethodPost, http.MethodPut:
		var req struct {
			SafetyBuffer           float64 `json:"safety_buffer"`
			DiscretionaryDailyBurn float64 `json:"discretionary_daily_burn"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid json payload", http.StatusBadRequest)
			return
		}

		if err := app.DB.SaveCashFlowSettings(username, req.SafetyBuffer, req.DiscretionaryDailyBurn); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"success": true, "safety_buffer": req.SafetyBuffer, "discretionary_daily_burn": req.DiscretionaryDailyBurn})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleForecastIncomesAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		incomes, err := app.DB.GetRecurringIncomes(username)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, incomes)

	case http.MethodPost:
		var req struct {
			Name        string  `json:"name"`
			Amount      float64 `json:"amount"`
			Frequency   string  `json:"frequency"`
			NextPayDate string  `json:"next_pay_date"`
			Category    string  `json:"category"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			req.Name = r.FormValue("name")
			req.Amount, _ = strconv.ParseFloat(r.FormValue("amount"), 64)
			req.Frequency = r.FormValue("frequency")
			req.NextPayDate = r.FormValue("next_pay_date")
			req.Category = r.FormValue("category")
		}

		if strings.TrimSpace(req.Name) == "" {
			jsonError(w, "income stream name is required", http.StatusBadRequest)
			return
		}
		if req.Amount <= 0 {
			jsonError(w, "amount must be greater than zero", http.StatusBadRequest)
			return
		}

		payDate := time.Now()
		if req.NextPayDate != "" {
			if parsed, err := time.Parse("2006-01-02", req.NextPayDate); err == nil {
				payDate = parsed
			}
		}

		income, err := app.DB.CreateRecurringIncome(username, req.Name, req.Frequency, req.Category, req.Amount, payDate)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, income)

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) HandleForecastIncomeByIDAPI(w http.ResponseWriter, r *http.Request) {
	username, ok := app.getSessionUser(r)
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/forecast/incomes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil {
		jsonError(w, "invalid income id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name        string  `json:"name"`
			Amount      float64 `json:"amount"`
			Frequency   string  `json:"frequency"`
			NextPayDate string  `json:"next_pay_date"`
			Category    string  `json:"category"`
			Status      string  `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid json payload", http.StatusBadRequest)
			return
		}

		payDate := time.Now()
		if req.NextPayDate != "" {
			if parsed, err := time.Parse("2006-01-02", req.NextPayDate); err == nil {
				payDate = parsed
			}
		}

		if err := app.DB.UpdateRecurringIncome(id, username, req.Name, req.Frequency, req.Category, req.Status, req.Amount, payDate); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"success": true})

	case http.MethodDelete:
		if err := app.DB.DeleteRecurringIncome(id, username); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"success": true})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

