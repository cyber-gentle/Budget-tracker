package app

import (
	"sync"
	"testing"
	"time"
)

func countWallets(t *testing.T, db *DBStore, username string) (total, defaults int) {
	t.Helper()
	accounts, err := db.GetAccounts(username)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if a.IsDefault {
			defaults++
		}
	}
	return len(accounts), defaults
}

func TestSignupCreatesStarterWallets(t *testing.T) {
	db := testStore(t)
	if err := db.Signup("ada", "ada@example.com", "secret1"); err != nil {
		t.Fatal(err)
	}
	var n int
	db.db.QueryRow("SELECT COUNT(*) FROM accounts WHERE username = 'ada'").Scan(&n)
	if n != 3 {
		t.Fatalf("signup created %d wallets, want 3", n)
	}
}

// A user without wallets (e.g. created before signup made them) whose first
// page load fires many concurrent requests must end up with exactly the three
// starter wallets and one default: no duplicates, no missing "Main Bank".
func TestDefaultWalletsUnderConcurrentFirstLoad(t *testing.T) {
	db := testStore(t)
	if _, err := db.db.Exec("INSERT INTO users (username, email, password, currency, created_at) VALUES ('old', 'old@example.com', 'x', '₦', ?)", time.Now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = db.GetAccounts("old") }()
	}
	wg.Wait()

	total, defaults := countWallets(t, db, "old")
	if total != 3 || defaults != 1 {
		t.Fatalf("got %d wallets with %d default, want 3 with 1 default", total, defaults)
	}
	accounts, _ := db.GetAccounts("old")
	if accounts[0].Name != "Main Bank" && !accounts[0].IsDefault {
		t.Fatalf("Main Bank missing or not default: %+v", accounts)
	}
}
