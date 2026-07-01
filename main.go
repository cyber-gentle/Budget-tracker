package main

import (
	"log"
	"net/http"
)

func main() {
	// Persistent storage (in project dir). You can change these paths if desired.
	app := NewApp("data/auth.json", "data/transactions.json")

	fileServer := http.FileServer(http.Dir("templates/"))
	
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("/templates/", http.StripPrefix("/templates/", fileServer))

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
	mux.HandleFunc("/api/transactions", app.HandleTransactions)               // GET, POST
	mux.HandleFunc("/api/transactions/", app.HandleDeleteTransaction)         // DELETE /api/transactions/{id}

	log.Println("Server started on http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

