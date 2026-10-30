package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestLayoutDataReadsStructsAndMaps(t *testing.T) {
	type pageData struct {
		Username string
		FullName string
		Email    string
	}
	got := layoutData("goals", pageData{Username: "ada", FullName: "Ada Okafor", Email: "ada@example.com"})
	if got["Active"] != "goals" || got["FullName"] != "Ada Okafor" || got["FirstName"] != "" {
		t.Fatalf("struct: %+v", got)
	}
	got = layoutData("receipts", map[string]any{"Username": "ada", "Email": "a@b.co"})
	if got["Username"] != "ada" || got["Email"] != "a@b.co" || got["FullName"] != "" {
		t.Fatalf("map: %+v", got)
	}
	if got := layoutData("x", nil); got["Username"] != "" {
		t.Fatalf("nil: %+v", got)
	}
}

// Every signed-in page renders with the shared layout, and the header marks
// exactly that page's links as active.
func TestPagesRenderSharedLayout(t *testing.T) {
	app, token := assistantTestApp(t)
	for _, page := range []string{"dashboard", "transactions", "debts", "subscriptions", "goals", "reports", "net-worth", "receipts", "forecast"} {
		req := httptest.NewRequest(http.MethodGet, "/"+page, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `id="btn-user-menu"`) || !strings.Contains(body, `id="btn-nav-mobile-more"`) {
			t.Fatalf("%s: status %d, shared header/nav missing", page, rec.Code)
		}
		if bytes.Count([]byte(body), []byte(`<header`)) != 1 {
			t.Fatalf("%s: expected exactly one header", page)
		}
	}
}

// Pages use the compiled stylesheet, never the in-browser Tailwind compiler, and
// every third-party script is version-pinned with a Subresource Integrity hash.
func TestPagesUseCompiledCSSAndPinnedScripts(t *testing.T) {
	app, token := assistantTestApp(t)
	scriptTag := regexp.MustCompile(`<script[^>]+src="https?://[^"]+"[^>]*>`)
	for _, page := range []string{"", "login", "sign-up", "dashboard", "transactions", "debts", "subscriptions", "goals", "reports", "net-worth", "receipts", "forecast"} {
		req := httptest.NewRequest(http.MethodGet, "/"+page, nil)
		if page != "" && page != "login" && page != "sign-up" {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		body := rec.Body.String()
		if strings.Contains(body, "cdn.tailwindcss.com") || strings.Contains(body, "tailwind.config") {
			t.Errorf("/%s still uses the Tailwind Play CDN", page)
		}
		if !strings.Contains(body, `href="/static/css/tailwind.css?v=`) {
			t.Errorf("/%s doesn't link the compiled stylesheet", page)
		}
		for _, tag := range scriptTag.FindAllString(body, -1) {
			if !strings.Contains(tag, `integrity="sha384-`) || !strings.Contains(tag, `crossorigin="anonymous"`) || !regexp.MustCompile(`@\d+\.\d+\.\d+/`).MatchString(tag) {
				t.Errorf("/%s has an unpinned or unverified external script: %s", page, tag)
			}
		}
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/css/tailwind.css", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") || !strings.Contains(rec.Body.String(), ".dark\\:bg-slate-950") {
		t.Fatalf("compiled stylesheet not served correctly: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
