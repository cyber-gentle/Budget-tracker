package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *DBStore {
	t.Helper()
	db, err := NewDBStore("file:"+filepath.Join(t.TempDir(), "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func receiptTestApp(t *testing.T) *App {
	t.Helper()
	t.Chdir(t.TempDir())
	db := testStore(t)
	for _, user := range []string{"alice", "bob"} {
		if _, err := db.db.Exec("INSERT INTO sessions(token,username,expires_at) VALUES (?,?,?)", user+"-token", user, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	return NewApp(db)
}

func receiptRequest(app *App, method, path, user, contentType string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if user != "" {
		r.AddCookie(&http.Cookie{Name: "session", Value: user + "-token"})
	}
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestReceiptFileOwnership(t *testing.T) {
	app := receiptTestApp(t)
	path := "/uploads/receipts/private.jpg"
	if err := os.WriteFile("uploads/receipts/private.jpg", []byte("private receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.CreateReceipt("alice", path, "private.jpg", "Shop", 10, 0, 0, nil, "food", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, path string
		status     int
	}{
		{"", path, 401}, {"bob", path, 404}, {"alice", path, 200},
		{"", "/uploads/receipts/", 401}, {"alice", "/uploads/receipts/", 404},
		{"alice", "/uploads/", 404}, {"alice", "/uploads/receipts/missing.jpg", 404},
	} {
		w := receiptRequest(app, "GET", tc.path, tc.user, "", nil)
		if w.Code != tc.status {
			t.Fatalf("user=%q path=%s status=%d want=%d", tc.user, tc.path, w.Code, tc.status)
		}
		if w.Code == 200 && (w.Body.String() != "private receipt" || w.Header().Get("Cache-Control") != "private, no-store") {
			t.Fatal("incorrect private response")
		}
	}
}

func TestMicrophonePermissionPolicy(t *testing.T) {
	app := receiptTestApp(t)
	w := receiptRequest(app, "GET", "/dashboard", "alice", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", w.Code)
	}
	want := "camera=(), microphone=(self), geolocation=(), payment=()"
	if got := w.Header().Get("Permissions-Policy"); got != want {
		t.Fatalf("Permissions-Policy = %q, want %q", got, want)
	}
}

func TestReceiptUploadLimits(t *testing.T) {
	app := receiptTestApp(t)
	for _, tc := range []struct {
		name   string
		size   int
		status int
	}{
		{"small", 32, 201}, {"at_limit", maxReceiptFileBytes, 201},
		{"over_file_limit", maxReceiptFileBytes + 1, 413}, {"over_body_limit", maxReceiptFileBytes + (2 << 20), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "receipt.jpg")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(bytes.Repeat([]byte("x"), tc.size)); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			w := receiptRequest(app, "POST", "/api/receipts/scan", "alice", writer.FormDataContentType(), body.Bytes())
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		name, data string
		status     int
	}{
		{"valid_base64", base64.StdEncoding.EncodeToString([]byte("image")), 201},
		{"invalid_base64", "!invalid!", 400},
		{"large_base64", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), maxReceiptFileBytes+1)), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"image_base64": tc.data})
			if err != nil {
				t.Fatal(err)
			}
			w := receiptRequest(app, "POST", "/api/receipts/scan", "alice", "application/json", body)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	// A valid JSON prefix must not bypass the request cap via trailing data.
	body := []byte(`{}` + strings.Repeat(" ", base64.StdEncoding.EncodedLen(maxReceiptFileBytes)+(2<<20)))
	if w := receiptRequest(app, "POST", "/api/receipts/scan", "alice", "application/json", body); w.Code != 413 {
		t.Fatalf("oversized JSON status=%d", w.Code)
	}
	files, err := os.ReadDir("uploads/receipts")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("rejected uploads left files: %d", len(files))
	}
	var count int
	if err := app.DB.db.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("rejected uploads created records: %d", count)
	}
}

func TestTrustedProxyClientIP(t *testing.T) {
	for _, tc := range []struct{ name, trusted, peer, xff, real, want string }{
		{"direct", "", "192.0.2.1:1234", "198.51.100.1", "203.0.113.1", "192.0.2.1"},
		{"untrusted_peer", "10.0.0.0/8", "192.0.2.1:1234", "198.51.100.1", "", "192.0.2.1"},
		{"trusted_chain", "10.0.0.0/8", "10.0.0.1:1234", "spoofed, 198.51.100.1, 10.0.0.2", "", "198.51.100.1"},
		{"invalid_header", "10.0.0.0/8", "10.0.0.1:1234", "arbitrary-value", "", "10.0.0.1"},
		{"real_ip", "10.0.0.0/8", "10.0.0.1:1234", "", "198.51.100.1", "198.51.100.1"},
		{"ipv6", "", "[2001:db8::1]:1234", "198.51.100.1", "", "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXY_CIDRS", tc.trusted)
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.xff)
			r.Header.Set("X-Real-IP", tc.real)
			if got := GetClientIP(r); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestSpoofedHeadersDoNotBypassLoginLimit(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	app := receiptTestApp(t)
	for i := 0; i < 21; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(""))
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if i == 20 && w.Code != 429 {
			t.Fatalf("status=%d want=429", w.Code)
		}
	}
}
