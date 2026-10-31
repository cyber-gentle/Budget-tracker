package app

import (
	"bytes"
	"io/fs"
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

// Basic accessibility guarantees that can be checked from the rendered HTML:
// a level-one heading, zoom allowed, and an accessible name on every <select>.
func TestPagesMeetBasicAccessibility(t *testing.T) {
	app, token := assistantTestApp(t)
	selectTag := regexp.MustCompile(`<select\b[^>]*>`)
	idAttr := regexp.MustCompile(`\bid="([^"]+)"`)
	for _, page := range []string{"", "login", "sign-up", "dashboard", "transactions", "debts", "subscriptions", "goals", "reports", "net-worth", "receipts", "forecast"} {
		req := httptest.NewRequest(http.MethodGet, "/"+page, nil)
		if page != "" && page != "login" && page != "sign-up" {
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, "<h1") {
			t.Errorf("/%s has no <h1>", page)
		}
		if strings.Contains(body, "user-scalable=no") || strings.Contains(body, "maximum-scale=1") {
			t.Errorf("/%s disables pinch-zoom", page)
		}
		for _, tag := range selectTag.FindAllString(body, -1) {
			if strings.Contains(tag, "aria-label=") {
				continue
			}
			m := idAttr.FindStringSubmatch(tag)
			if m == nil || !strings.Contains(body, `for="`+m[1]+`"`) {
				t.Errorf("/%s: <select> without an accessible name: %s", page, tag[:min(len(tag), 80)])
			}
		}
	}
}

// Colour classes that fail WCAG AA (4.5:1) as text on Spendly's surfaces. The
// AA replacements are listed in the phase 5 colour roles: slate-500 / dark:slate-400
// for secondary text, -700 shades for money and status text, emerald-700 buttons.
func TestTemplatesAvoidLowContrastColours(t *testing.T) {
	banned := regexp.MustCompile(`(?:^|[\s"'\x60])(text-slate-400|dark:text-slate-(?:500|600)|text-(?:emerald|rose|amber)-[56]00|text-gray-400)(?:[\s"'\x60]|$)`)
	whiteOnLightGreen := regexp.MustCompile(`(?:^|[\s"'\x60])bg-emerald-[56]00(?:[\s"'\x60]|$)[^"'\x60]*\stext-white|\stext-white\s[^"'\x60]*(?:^|\s)bg-emerald-[56]00(?:[\s"'\x60]|$)`)
	err := fs.WalkDir(embeddedFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".js")) {
			return err
		}
		data, _ := embeddedFS.ReadFile(p)
		for i, line := range strings.Split(string(data), "\n") {
			if m := banned.FindStringSubmatch(line); m != nil {
				t.Errorf("%s:%d uses low-contrast %s", p, i+1, m[1])
			}
			if whiteOnLightGreen.MatchString(line) {
				t.Errorf("%s:%d puts white text on emerald-500/600 (use bg-emerald-700)", p, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
