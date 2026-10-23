package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAIProviderSelection(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "test-server-key")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, tc := range []struct{ provider, key, host string }{
		{"local", "", ""}, {"openai", "test-key", "api.openai.com"},
		{"gemini", "test-key", "generativelanguage.googleapis.com"},
		{"hybrid", "", "api.groq.com"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var hosts []string
			http.DefaultTransport = testTransport(func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				body := `{"choices":[{"message":{"content":"{\"amount\":4500}"}}]}`
				if tc.provider == "gemini" {
					body = `{"candidates":[{"content":{"parts":[{"text":"{\"amount\":4500}"}]}}]}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			got := ParseAITransaction("Paid NGN 4500 for food", nil, &AISettings{Provider: tc.provider, APIKey: tc.key})
			if got.Amount != 4500 {
				t.Fatalf("amount = %v", got.Amount)
			}
			if tc.host == "" {
				if len(hosts) != 0 {
					t.Fatalf("offline request reached %v", hosts)
				}
			} else if len(hosts) != 1 || hosts[0] != tc.host {
				t.Fatalf("requests = %v, want %s", hosts, tc.host)
			}
		})
	}
}

func TestCurrencyAmountParsing(t *testing.T) {
	for _, tc := range []struct {
		text   string
		amount float64
	}{
		{"Paid NGN 4500 for food", 4500}, {"Paid $2500 for food", 2500},
		{"Paid ₦15000 for food", 15000}, {"Paid ngn 4500.50 for food", 4500.5},
		{"Paid €12,345.67", 12345.67}, {"Paid £250", 250}, {"Paid 15k", 15000},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := parseWithLocalNLP(tc.text, nil).Amount; got != tc.amount {
				t.Fatalf("amount = %v, want %v", got, tc.amount)
			}
		})
	}
}
