package handler

import (
	"encoding/json"
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
	// Try multiple sources for the original client route
	var finalPath string

	if matched := getHeaderCaseInsensitive(r, "x-matched-path"); matched != "" && !strings.HasPrefix(matched, "/api/index") && matched != "/api" {
		if u, err := url.Parse(matched); err == nil && u.Path != "" {
			finalPath = u.Path
		}
	}

	if finalPath == "" {
		if qPath := r.URL.Query().Get("__path"); qPath != "" {
			finalPath = qPath
			q := r.URL.Query()
			q.Del("__path")
			r.URL.RawQuery = q.Encode()
		}
	}

	if finalPath == "" {
		if origURI := getHeaderCaseInsensitive(r, "x-forwarded-uri"); origURI != "" && !strings.HasPrefix(origURI, "/api/index") && origURI != "/api" {
			if u, err := url.Parse(origURI); err == nil && u.Path != "" {
				finalPath = u.Path
			}
		}
	}

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

	if finalPath == "" || finalPath == "/index" || finalPath == "/index.html" || finalPath == "/api/index" {
		finalPath = "/"
	}
	r.URL.Path = finalPath

	if r.URL.Query().Get("__debug") == "1" {
		w.Header().Set("Content-Type", "application/json")
		headers := make(map[string][]string)
		for k, v := range r.Header {
			headers[k] = v
		}
		data := map[string]any{
			"url_path":    r.URL.Path,
			"raw_query":   r.URL.RawQuery,
			"final_path":  finalPath,
			"request_uri": r.RequestURI,
			"headers":     headers,
		}
		json.NewEncoder(w).Encode(data)
		return
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
