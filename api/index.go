package handler

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"spendly/internal/app"
)

var (
	server  *app.App
	once    sync.Once
	initErr error
)

func getHeaderCaseInsensitive(r *http.Request, key string) string {
	if val := r.Header.Get(key); val != "" {
		return val
	}
	keyLower := strings.ToLower(key)
	for k, v := range r.Header {
		if strings.ToLower(k) == keyLower && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func Handler(w http.ResponseWriter, r *http.Request) {
	// Restore clean request path
	p := r.URL.Path

	// Strip /api/index.go or /api/index prefix if present from rewrite
	if strings.HasPrefix(p, "/api/index.go") {
		p = strings.TrimPrefix(p, "/api/index.go")
	} else if strings.HasPrefix(p, "/api/index") {
		p = strings.TrimPrefix(p, "/api/index")
	}

	// Also check forwarded URI header as secondary verification
	if p == "" || p == "/" {
		if origURI := getHeaderCaseInsensitive(r, "x-forwarded-uri"); origURI != "" && origURI != "/api/index" {
			if u, err := url.Parse(origURI); err == nil && u.Path != "" {
				p = u.Path
			}
		}
	}

	if p == "" || p == "/index" || p == "/index.html" {
		p = "/"
	}
	r.URL.Path = p

	once.Do(func() {
		db, err := app.NewDBStore(os.Getenv("TURSO_DATABASE_URL"), os.Getenv("TURSO_AUTH_TOKEN"))
		if err != nil {
			initErr = err
			return
		}
		server = app.NewApp(db)
	})

	if initErr != nil {
		http.Error(w, "Database initialization error: "+initErr.Error(), http.StatusInternalServerError)
		return
	}

	server.ServeHTTP(w, r)
}
