package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// App holds the shared application state.
type App struct {
	Auth       *AuthStore
	Trackers   map[string]*BudgetTracker
	TrackersMu sync.RWMutex
	DataDir    string
	Tmpl       *template.Template
}

// NewApp creates an App, parsing templates and initialising stores.
func NewApp(authPath, dataDir string) *App {
	auth := NewAuthStore(authPath)
	tmpl := template.Must(template.ParseGlob("templates/html/*.html"))
	return &App{
		Auth:     auth,
		Trackers: make(map[string]*BudgetTracker),
		DataDir:  dataDir,
		Tmpl:     tmpl,
	}
}

// trackerFor returns (creating if needed) the BudgetTracker for a user.
func (app *App) trackerFor(username string) *BudgetTracker {
	app.TrackersMu.RLock()
	bt, ok := app.Trackers[username]
	app.TrackersMu.RUnlock()
	if ok {
		return bt
	}
	bt = NewBudgetTracker(app.DataDir + "/" + username + "_transactions.json")
	app.TrackersMu.Lock()
	app.Trackers[username] = bt
	app.TrackersMu.Unlock()
	return bt
}

// renderTemplate is a helper to execute a named template.
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
	return app.Auth.ValidateSession(c.Value)
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

	tracker := app.trackerFor(username)
	txs := tracker.GetTransactions()
	// Reverse so newest first.
	for i, j := 0, len(txs)-1; i < j; i, j = i+1, j-1 {
		txs[i], txs[j] = txs[j], txs[i]
	}

	type DisplayTx struct {
		ID        int
		Amount    float64
		AmountFmt string
		Category  string
		Note      string
		DateStr   string
		Type      string
		Sign      string
		Color     string
	}

	var display []DisplayTx
	for _, t := range txs {
		sign := "+"
		color := "green"
		if t.Type == "expense" {
			sign = "-"
			color = "red"
		}
		display = append(display, DisplayTx{
			ID:        t.ID,
			Amount:    t.Amount,
			AmountFmt: formatNaira(t.Amount),
			Category:  t.Category,
			Note:      t.Note,
			DateStr:   t.Date.Format("Jan 2"),
			Type:      t.Type,
			Sign:      sign,
			Color:     color,
		})
	}

	income := tracker.CalculateTotal("income")
	expenses := tracker.CalculateTotal("expense")
	balance := tracker.Balance()

	data := struct {
		Username     string
		IncomeFmt    string
		ExpensesFmt  string
		BalanceFmt   string
		Transactions []DisplayTx
	}{
		Username:     username,
		IncomeFmt:    formatNaira(income),
		ExpensesFmt:  formatNaira(expenses),
		BalanceFmt:   formatNaira(balance),
		Transactions: display,
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

	if err := app.Auth.Signup(username, password); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	token, err := app.Auth.Login(username, password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 86400,
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

	token, err := app.Auth.Login(username, password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 86400,
	})
	jsonOK(w, map[string]string{"redirect": "/dashboard"})
}

func (app *App) HandleLogoutAPI(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("session")
	if err == nil {
		app.Auth.Logout(c.Value)
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
	tracker := app.trackerFor(username)
	switch r.Method {
	case http.MethodGet:
		txs := tracker.GetTransactions()
		for i, j := 0, len(txs)-1; i < j; i, j = i+1, j-1 {
			txs[i], txs[j] = txs[j], txs[i]
		}
		jsonOK(w, txs)
	case http.MethodPost:
		r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		note := strings.TrimSpace(r.FormValue("note"))
		txnType := strings.TrimSpace(r.FormValue("type"))
		if amountStr == "" || txnType == "" {
			jsonError(w, "amount and type are required", http.StatusBadRequest)
			return
		}
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}
		tracker.AddTransaction(amount, category, note, txnType)
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
	tracker := app.trackerFor(username)
	switch r.Method {
	case http.MethodPut:
		r.ParseMultipartForm(1 << 20)
		amountStr := strings.TrimSpace(r.FormValue("amount"))
		category := strings.TrimSpace(r.FormValue("category"))
		note := strings.TrimSpace(r.FormValue("note"))
		txnType := strings.TrimSpace(r.FormValue("type"))
		if amountStr == "" || txnType == "" {
			jsonError(w, "amount and type are required", http.StatusBadRequest)
			return
		}
		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil || amount <= 0 {
			jsonError(w, "invalid amount", http.StatusBadRequest)
			return
		}
		if !tracker.UpdateTransaction(id, amount, category, note, txnType) {
			jsonError(w, "transaction not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "updated"})
	case http.MethodDelete:
		if !tracker.DeleteTransaction(id) {
			jsonError(w, "transaction not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func formatNaira(v float64) string {
	return fmt.Sprintf("₦%s", comma(int(v)))
}

func comma(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
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
	return strings.Join(parts, ",")
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
