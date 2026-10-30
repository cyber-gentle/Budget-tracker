package app

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(app *App, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestPagesAreGzippedWhenAccepted(t *testing.T) {
	app, _ := assistantTestApp(t)

	rec := get(app, "/login", map[string]string{"Accept-Encoding": "gzip, br"})
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("login not gzipped: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(zr)
	if !strings.Contains(string(html), "</html>") {
		t.Fatal("gzipped body did not decode to the page")
	}

	if rec := get(app, "/login", nil); rec.Header().Get("Content-Encoding") != "" || !strings.Contains(rec.Body.String(), "</html>") {
		t.Fatal("client without gzip support got a compressed or broken response")
	}
	// Tiny bodies with a known length aren't worth compressing.
	if rec := get(app, "/healthz", map[string]string{"Accept-Encoding": "gzip"}); rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("tiny response was compressed")
	}
}

func TestStaticAssetsAreVersionedCachedAndScoped(t *testing.T) {
	app, _ := assistantTestApp(t)

	page := get(app, "/login", nil).Body.String()
	m := regexp.MustCompile(`/static/css/tailwind\.css\?v=([0-9a-f]{12})`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("page doesn't link a versioned stylesheet")
	}

	rec := get(app, m[0], nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || rec.Header().Get("ETag") != `"`+m[1]+`"` {
		t.Fatalf("versioned asset headers: %d %v", rec.Code, rec.Header())
	}
	// Stale or missing version: must revalidate rather than be cached for a year.
	if rec := get(app, "/static/css/tailwind.css?v=old", nil); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("stale version cached: %v", rec.Header())
	}
	if rec := get(app, "/static/css/tailwind.css", map[string]string{"If-None-Match": `"` + m[1] + `"`}); rec.Code != http.StatusNotModified {
		t.Fatalf("ETag revalidation: want 304, got %d", rec.Code)
	}

	// Only asset files are public: no directory listings or template sources.
	for _, p := range []string{"/static/", "/static/css/", "/static/html/dashboard.html", "/static/html/login.html"} {
		if rec := get(app, p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: want 404, got %d", p, rec.Code)
		}
	}
	// Dot-segments are cleaned by the router (redirect), never served directly.
	if rec := get(app, "/static/js/../html/login.html", nil); rec.Code == http.StatusOK {
		t.Error("dot-segment path served a template")
	}
}
