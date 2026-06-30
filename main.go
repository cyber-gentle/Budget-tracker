package main

import (
	"html/template"
	"log"
	"net/http"
)

var tmpl = template.Must(template.ParseGlob("templates/html/*.html"))

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "index.html", nil)
}

func SignUpHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "sign-up.html", nil)
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "login.html", nil)
}

func SwitchHandler() {
	
}

func Server() {

	fileServer := http.FileServer(http.Dir("templates/assets/"))
	http.Handle("/templates/assets/imgs", http.StripPrefix("/templates/assets/imgs/", fileServer))

	mainMux := http.NewServeMux()
	apiMux := http.NewServeMux()

	mainMux.HandleFunc("/", HomeHandler)
	mainMux.HandleFunc("/sign-up.html", SignUpHandler)
	mainMux.Handle("/api/", http.StripPrefix("/api", apiMux))

	log.Println("Server started on http://localhost:8080")
	http.ListenAndServe(":8080", mainMux)
}

func renderTemplate(w http.ResponseWriter, filename string, data any) {
	err := tmpl.ExecuteTemplate(w, filename, data)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Printf("Error rendering template: %v", err)
	}
}

func main() {
	Server()
}
