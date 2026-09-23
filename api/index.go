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
	// 1. Check if rewritten path was passed via __path query parameter
	q := r.URL.Query()
	targetPath := q.Get("__path")

	if targetPath != "" {
		// Clean up __path from query string so downstream handlers don't see it
		q.Del("__path")
		r.URL.RawQuery = q.Encode()
		r.URL.Path = targetPath
	} else if origURI := getHeaderCaseInsensitive(r, "x-forwarded-uri"); origURI != "" && origURI != "/api/index" && origURI != "/api" {
		if u, err := url.Parse(origURI); err == nil && u.Path != "" {
			r.URL.Path = u.Path
		}
	} else {
		// Fallback: strip /api/index.go or /api/index
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/index.go") {
			p = strings.TrimPrefix(p, "/api/index.go")
		} else if strings.HasPrefix(p, "/api/index") {
			p = strings.TrimPrefix(p, "/api/index")
		} else if strings.HasPrefix(p, "/api") {
			p = strings.TrimPrefix(p, "/api")
		}
		r.URL.Path = p
	}

	if r.URL.Path == "" || r.URL.Path == "/index" || r.URL.Path == "/index.html" || r.URL.Path == "/api/index" {
		r.URL.Path = "/"
	}

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
