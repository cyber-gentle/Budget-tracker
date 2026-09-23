package main

import (
	"log"
	"net/http"
	"os"

	"spendly/internal/app"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	db, err := app.NewDBStore(os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN"))
	if err != nil {
		log.Fatalf("database error: %v", err)
	}

	server := app.NewApp(db)

	log.Printf("Spendly server running on port :%s", port)
	if err := http.ListenAndServe(":"+port, server); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
