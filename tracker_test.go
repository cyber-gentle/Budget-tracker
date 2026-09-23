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

func TestTrendsTimeframes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_trends.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	err = store.Signup("trenduser", "trend@spendly.app", "securepass123")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	// Add transactions spanning multiple months
	now := time.Now()
	// 1. Transaction in current month
	_ = store.AddTransaction("trenduser", 10000, "salary", "This Month Salary", "income", now)
	_ = store.AddTransaction("trenduser", 2500, "food", "Groceries", "expense", now)

	// 2. Transaction 2 months ago
	d2 := now.AddDate(0, -2, 0)
	_ = store.AddTransaction("trenduser", 8000, "salary", "2 Months Ago Salary", "income", d2)
	_ = store.AddTransaction("trenduser", 3000, "transport", "Fuel", "expense", d2)

	// 3. Transaction 5 months ago
	d5 := now.AddDate(0, -5, 0)
	_ = store.AddTransaction("trenduser", 7500, "salary", "5 Months Ago Salary", "income", d5)

	// Test 3m
	trends3m, err := store.GetTrendsByTimeFrame("trenduser", "3m", "", "")
	if err != nil {
		t.Fatalf("3m failed: %v", err)
	}
	if len(trends3m) != 3 {
		t.Fatalf("expected 3 periods for 3m, got %d", len(trends3m))
	}
	// Last element is current month
	if trends3m[len(trends3m)-1].Income != 10000 || trends3m[len(trends3m)-1].Expense != 2500 {
		t.Fatalf("current month trend mismatch in 3m: %+v", trends3m[len(trends3m)-1])
	}

	// Test 6m
	trends6m, err := store.GetTrendsByTimeFrame("trenduser", "6m", "", "")
	if err != nil {
		t.Fatalf("6m failed: %v", err)
	}
	if len(trends6m) != 6 {
		t.Fatalf("expected 6 periods for 6m, got %d", len(trends6m))
	}

	// Test YTD
	trendsYTD, err := store.GetTrendsByTimeFrame("trenduser", "ytd", "", "")
	if err != nil {
		t.Fatalf("ytd failed: %v", err)
	}
	if len(trendsYTD) != int(now.Month()) {
		t.Fatalf("expected %d periods for ytd, got %d", int(now.Month()), len(trendsYTD))
	}

	// Test All Time
	trendsAll, err := store.GetTrendsByTimeFrame("trenduser", "all", "", "")
	if err != nil {
		t.Fatalf("all failed: %v", err)
	}
	if len(trendsAll) < 6 {
		t.Fatalf("expected at least 6 periods for all time, got %d", len(trendsAll))
	}

	// Test Custom Short Range (daily aggregation <= 31 days)
	startStr := now.AddDate(0, 0, -5).Format("2006-01-02")
	endStr := now.Format("2006-01-02")
	trendsCustomDaily, err := store.GetTrendsByTimeFrame("trenduser", "custom", startStr, endStr)
	if err != nil {
		t.Fatalf("custom daily failed: %v", err)
	}
	if len(trendsCustomDaily) != 6 { // 6 days inclusive
		t.Fatalf("expected 6 days for daily custom range, got %d", len(trendsCustomDaily))
	}

	// Test Custom Multi-Month Range (> 31 days)
	startMStr := now.AddDate(0, -3, 0).Format("2006-01-02")
	endMStr := now.Format("2006-01-02")
	trendsCustomMonthly, err := store.GetTrendsByTimeFrame("trenduser", "custom", startMStr, endMStr)
	if err != nil {
		t.Fatalf("custom monthly failed: %v", err)
	}
	if len(trendsCustomMonthly) != 4 { // 4 months inclusive
		t.Fatalf("expected 4 months for custom range, got %d", len(trendsCustomMonthly))
	}
}

