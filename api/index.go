package handler

import (
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
	var finalPath string

	// 1. Primary: check __path passed by vercel.json rewrite
	q := r.URL.Query()
	if qPath := q.Get("__path"); qPath != "" {
		finalPath = qPath
		q.Del("__path")
		r.URL.RawQuery = q.Encode()
	}

	// 2. Secondary: check x-forwarded-uri
	if finalPath == "" {
		if origURI := getHeaderCaseInsensitive(r, "x-forwarded-uri"); origURI != "" && !strings.HasPrefix(origURI, "/api/index") && origURI != "/api" {
			if u, err := url.Parse(origURI); err == nil && u.Path != "" {
				finalPath = u.Path
			}
		}
	}

	// 3. Tertiary: check x-matched-path (only if not root or api index)
	if finalPath == "" {
		if matched := getHeaderCaseInsensitive(r, "x-matched-path"); matched != "" && matched != "/" && !strings.HasPrefix(matched, "/api/index") && matched != "/api" {
			if u, err := url.Parse(matched); err == nil && u.Path != "" {
				finalPath = u.Path
			}
		}
	}

	// 4. Fallback: check r.URL.Path
	if finalPath == "" {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/index.go") {
			p = strings.TrimPrefix(p, "/api/index.go")
		} else if strings.HasPrefix(p, "/api/index") {
			p = strings.TrimPrefix(p, "/api/index")
		} else if strings.HasPrefix(p, "/api") {
			p = strings.TrimPrefix(p, "/api")
		}
		finalPath = p
	}

	// Normalize path (ensure leading slash, resolve double slashes, clean)
	if finalPath != "" {
		finalPath = path.Clean("/" + strings.TrimLeft(finalPath, "/"))
	}

	if finalPath == "" || finalPath == "/index" || finalPath == "/index.html" || finalPath == "/api" || finalPath == "/api/index" || finalPath == "/api/index.go" {
		finalPath = "/"
	}
	r.URL.Path = finalPath

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
