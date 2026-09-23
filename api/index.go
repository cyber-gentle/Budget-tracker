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
	// Restore original request path when rewritten by Vercel
	origURI := getHeaderCaseInsensitive(r, "x-forwarded-uri")
	matchedPath := getHeaderCaseInsensitive(r, "x-matched-path")

	if origURI != "" {
		if u, err := url.Parse(origURI); err == nil {
			r.URL.Path = u.Path
			if u.RawQuery != "" {
				r.URL.RawQuery = u.RawQuery
			}
		}
	} else if matchedPath != "" && matchedPath != "/api" && matchedPath != "/api/index.go" {
		r.URL.Path = matchedPath
	} else {
		// Fallback: strip /api/index.go or /api if present
		if strings.HasPrefix(r.URL.Path, "/api/index.go") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/index.go")
		} else if strings.HasPrefix(r.URL.Path, "/api") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		}
	}

	if r.URL.Path == "" {
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
