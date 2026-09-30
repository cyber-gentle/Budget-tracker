package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"
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

// Handler is the Vercel serverless function entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	// Diagnostic check
	if r.URL.Query().Get("__debug") == "1" {
		w.Header().Set("Content-Type", "application/json")
		headers := make(map[string][]string)
		for k, v := range r.Header {
			headers[k] = v
		}
		json.NewEncoder(w).Encode(map[string]any{
			"url_path":    r.URL.Path,
			"raw_query":   r.URL.RawQuery,
			"request_uri": r.RequestURI,
			"headers":     headers,
		})
		return
	}

	origPath := r.URL.Path

	// If Vercel rewrote path to function file itself, recover target route
	if origPath == "" || strings.HasPrefix(origPath, "/api/index") || origPath == "/api" {
		if fURI := getHeaderCaseInsensitive(r, "x-forwarded-uri"); fURI != "" && !strings.HasPrefix(fURI, "/api/index") {
			if u, err := url.Parse(fURI); err == nil && u.Path != "" {
				origPath = u.Path
			}
		} else if mPath := getHeaderCaseInsensitive(r, "x-matched-path"); mPath != "" && mPath != "/" && !strings.HasPrefix(mPath, "/api/index") {
			if u, err := url.Parse(mPath); err == nil && u.Path != "" {
				origPath = u.Path
			}
		} else if qPath := r.URL.Query().Get("__path"); qPath != "" {
			origPath = qPath
			q := r.URL.Query()
			q.Del("__path")
			r.URL.RawQuery = q.Encode()
		} else {
			origPath = "/"
		}
	}

	// Normalize route
	origPath = path.Clean("/" + strings.TrimLeft(origPath, "/"))
	if origPath == "/index" || origPath == "/index.html" {
		origPath = "/"
	}
	r.URL.Path = origPath

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
