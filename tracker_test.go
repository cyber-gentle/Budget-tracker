package main

import (
	"path/filepath"
	"testing"
	"time"

	"spendly/internal/app"
)

func TestDBFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	// 1. Auth Flow
	err = store.Signup("testuser", "test@spendly.app", "securepass123")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	// Duplicate username should fail
	if err := store.Signup("testuser", "other@spendly.app", "anotherpass"); err == nil {
		t.Fatalf("expected duplicate username signup to fail")
	}

	// Duplicate email should fail
	if err := store.Signup("newuser", "test@spendly.app", "anotherpass"); err == nil {
		t.Fatalf("expected duplicate email signup to fail")
	}

	// Login with username
	token1, err := store.Login("testuser", "securepass123")
	if err != nil || token1 == "" {
		t.Fatalf("login with username failed: %v", err)
	}

	// Login with email
	token2, err := store.Login("test@spendly.app", "securepass123")
	if err != nil || token2 == "" {
		t.Fatalf("login with email failed: %v", err)
	}

	// Validate Session
	username, ok := store.ValidateSession(token2)
	if !ok || username != "testuser" {
		t.Fatalf("expected valid session for testuser from email login, got %v (ok=%v)", username, ok)
	}

	// 2. Transactions Flow
	d1 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	err = store.AddTransaction("testuser", 50000, "salary", "Monthly Salary", "income", d1)
	if err != nil {
		t.Fatalf("failed to add income: %v", err)
	}

	d2 := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	err = store.AddTransaction("testuser", 15000, "food", "Groceries", "expense", d2)
	if err != nil {
		t.Fatalf("failed to add expense: %v", err)
	}

	// Check totals
	inc, exp, bal, err := store.CalculateTotals("testuser")
	if err != nil {
		t.Fatalf("calculate totals failed: %v", err)
	}
	if inc != 50000 || exp != 15000 || bal != 35000 {
		t.Fatalf("unexpected totals: inc=%v, exp=%v, bal=%v", inc, exp, bal)
	}

	txs, err := store.GetTransactions("testuser")
	if err != nil || len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d (err=%v)", len(txs), err)
	}

	// Update transaction
	txId := txs[0].ID
	if txs[0].Type == "income" {
		txId = txs[1].ID
	}
	updated, err := store.UpdateTransaction(txId, "testuser", 18000, "food", "Groceries + Snacks", "expense", d2)
	if err != nil || !updated {
		t.Fatalf("failed to update tx %d: %v", txId, err)
	}

	// Check updated totals
	_, exp, bal, _ = store.CalculateTotals("testuser")
	if exp != 18000 || bal != 32000 {
		t.Fatalf("unexpected updated totals: exp=%v, bal=%v", exp, bal)
	}

	// 3. Budgets Flow
	err = store.SetBudget("testuser", "food", 30000)
	if err != nil {
		t.Fatalf("failed to set budget: %v", err)
	}

	budgets, err := store.GetBudgets("testuser")
	if err != nil || budgets["food"] != 30000 {
		t.Fatalf("expected food budget 30000, got %v", budgets["food"])
	}

	// 4. Delete Transaction
	deleted, err := store.DeleteTransaction(txId, "testuser")
	if err != nil || !deleted {
		t.Fatalf("failed to delete tx %d", txId)
	}
	txs, _ = store.GetTransactions("testuser")
	if len(txs) != 1 {
		t.Fatalf("expected 1 remaining transaction, got %d", len(txs))
	}

	// 5. Logout
	store.Logout(token2)
	if _, ok := store.ValidateSession(token2); ok {
		t.Fatalf("expected session to be invalidated after logout")
	}
}
