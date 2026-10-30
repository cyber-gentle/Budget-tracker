package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
