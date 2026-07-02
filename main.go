package main

import (
	"log"
	"net/http"
)

func main() {
	app := NewApp("data/auth.json", "data")

	mux := http.NewServeMux()

	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("templates/"))))

	// Pages
	mux.HandleFunc("/", app.HandleHome)
	mux.HandleFunc("/login", app.HandleLogin)
	mux.HandleFunc("/sign-up", app.HandleSignUp)
	mux.HandleFunc("/dashboard", app.HandleDashboard)

	// Auth APIs
	mux.HandleFunc("/api/signup", app.HandleSignupAPI)
	mux.HandleFunc("/api/login", app.HandleLoginAPI)
	mux.HandleFunc("/api/logout", app.HandleLogoutAPI)

	// Transaction APIs
	mux.HandleFunc("/api/transactions", app.HandleTransactions)
	mux.HandleFunc("/api/transactions/", app.HandleTransactionByID)

	log.Println("Server started on http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
