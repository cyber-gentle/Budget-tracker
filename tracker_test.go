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
	err = store.Signup("testuser", "test@spendly.app", "securepass123", "Test User")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	prof, err := store.GetProfile("testuser")
	if err != nil || prof.FullName != "Test User" {
		t.Fatalf("expected FullName 'Test User', got %q (err: %v)", prof.FullName, err)
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

func TestUserProfileFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_profile.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	// 1. Signup two users
	err = store.Signup("alice", "alice@example.com", "alicepass123")
	if err != nil {
		t.Fatalf("signup alice failed: %v", err)
	}
	err = store.Signup("bob", "bob@example.com", "bobpass123")
	if err != nil {
		t.Fatalf("signup bob failed: %v", err)
	}

	// 2. Fetch initial profile
	prof, err := store.GetProfile("alice")
	if err != nil {
		t.Fatalf("get profile failed: %v", err)
	}
	if prof.Username != "alice" || prof.Email != "alice@example.com" || prof.Currency != "₦" {
		t.Fatalf("unexpected initial profile: %+v", prof)
	}

	// 3. Update profile (Name, Email, Currency)
	err = store.UpdateProfile("alice", "Alice Wonderland", "alice.new@example.com", "$", "", "")
	if err != nil {
		t.Fatalf("update profile failed: %v", err)
	}

	profUpdated, err := store.GetProfile("alice")
	if err != nil {
		t.Fatalf("get updated profile failed: %v", err)
	}
	if profUpdated.FullName != "Alice Wonderland" || profUpdated.Email != "alice.new@example.com" || profUpdated.Currency != "$" {
		t.Fatalf("unexpected updated profile: %+v", profUpdated)
	}

	// 4. Duplicate email prevention across accounts
	err = store.UpdateProfile("bob", "Bob Ross", "alice.new@example.com", "$", "", "")
	if err == nil {
		t.Fatalf("expected duplicate email update to fail for bob")
	}

	// 5. Change password validation
	// 5a. Incorrect current password
	err = store.UpdateProfile("alice", "Alice Wonderland", "alice.new@example.com", "$", "wrongpass", "brandnewpass123")
	if err == nil {
		t.Fatalf("expected password change with incorrect current password to fail")
	}

	// 5b. Short new password
	err = store.UpdateProfile("alice", "Alice Wonderland", "alice.new@example.com", "$", "alicepass123", "short")
	if err == nil {
		t.Fatalf("expected password change with short new password to fail")
	}

	// 5c. Successful password change
	err = store.UpdateProfile("alice", "Alice Wonderland", "alice.new@example.com", "$", "alicepass123", "brandnewpass123")
	if err != nil {
		t.Fatalf("successful password update failed: %v", err)
	}

	// 6. Verify login with old vs new password
	if _, err := store.Login("alice", "alicepass123"); err == nil {
		t.Fatalf("expected login with old password to fail")
	}
	token, err := store.Login("alice", "brandnewpass123")
	if err != nil || token == "" {
		t.Fatalf("expected login with new password to succeed, got %v", err)
	}
}

func TestDebtsFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "debts_test.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "david"
	_ = store.Signup(username, "david@example.com", "pass123456")

	// 1. Create Debts
	// a. Someone owes david 50,000
	dueDate := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	debt1, err := store.CreateDebt(username, "John Doe", "owing_me", 50000, &dueDate, "Freelance project balance")
	if err != nil {
		t.Fatalf("create owing_me debt failed: %v", err)
	}
	if debt1.Amount != 50000 || debt1.Status != "unpaid" || debt1.Remaining != 50000 {
		t.Fatalf("unexpected debt1 values: %+v", debt1)
	}

	// b. David owes Mike 20,000
	debt2, err := store.CreateDebt(username, "Mike Ross", "i_owe", 20000, nil, "Laptop screen repair")
	if err != nil {
		t.Fatalf("create i_owe debt failed: %v", err)
	}
	if debt2.Amount != 20000 || debt2.Status != "unpaid" || debt2.Remaining != 20000 {
		t.Fatalf("unexpected debt2 values: %+v", debt2)
	}

	// 2. Summary Check
	debts, summary, err := store.GetDebts(username)
	if err != nil {
		t.Fatalf("get debts failed: %v", err)
	}
	if len(debts) != 2 {
		t.Fatalf("expected 2 debts, got %d", len(debts))
	}
	if summary.TotalOwedToUser != 50000 || summary.TotalUserOwes != 20000 || summary.NetBalance != 30000 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.CountOwingMe != 1 || summary.CountIOwe != 1 {
		t.Fatalf("unexpected active counts: %+v", summary)
	}

	// 3. Partial Payment on owing_me with transaction logging
	payDate := time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)
	updated1, err := store.RecordDebtPayment(debt1.ID, username, 20000, payDate, true)
	if err != nil {
		t.Fatalf("record debt payment failed: %v", err)
	}
	if updated1.AmountPaid != 20000 || updated1.Remaining != 30000 || updated1.Status != "partial" {
		t.Fatalf("unexpected updated1 debt: %+v", updated1)
	}

	// Verify main transaction was logged as INCOME
	txs, err := store.GetTransactions(username)
	if err != nil {
		t.Fatalf("get transactions failed: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 logged transaction, got %d", len(txs))
	}
	if txs[0].Type != "income" || txs[0].Amount != 20000 {
		t.Fatalf("expected 20000 income transaction, got %+v", txs[0])
	}

	// 4. Settle remaining 30,000 on debt1
	updated1Settle, err := store.RecordDebtPayment(debt1.ID, username, 30000, payDate, true)
	if err != nil {
		t.Fatalf("record final debt payment failed: %v", err)
	}
	if updated1Settle.AmountPaid != 50000 || updated1Settle.Remaining != 0 || updated1Settle.Status != "settled" {
		t.Fatalf("unexpected settled debt: %+v", updated1Settle)
	}

	// 5. Pay off debt2 (i_owe) with transaction logging -> should log EXPENSE
	updated2, err := store.RecordDebtPayment(debt2.ID, username, 20000, payDate, true)
	if err != nil {
		t.Fatalf("record debt2 payment failed: %v", err)
	}
	if updated2.Status != "settled" || updated2.Remaining != 0 {
		t.Fatalf("unexpected debt2 settled: %+v", updated2)
	}

	// Verify main transaction was logged as EXPENSE
	txs, err = store.GetTransactions(username)
	if err != nil {
		t.Fatalf("get transactions failed: %v", err)
	}
	if len(txs) != 3 { // 2 income repayments from debt1, 1 expense from debt2
		t.Fatalf("expected 3 transactions, got %d", len(txs))
	}
	foundExpense := false
	for _, tItem := range txs {
		if tItem.Type == "expense" && tItem.Amount == 20000 {
			foundExpense = true
			break
		}
	}
	if !foundExpense {
		t.Fatalf("expected 20000 expense transaction logged from debt2 payment")
	}

	// 6. Verify final summary
	_, finalSummary, err := store.GetDebts(username)
	if err != nil {
		t.Fatalf("get final debts failed: %v", err)
	}
	if finalSummary.TotalOwedToUser != 0 || finalSummary.TotalUserOwes != 0 || finalSummary.TotalSettled != 2 {
		t.Fatalf("unexpected final summary: %+v", finalSummary)
	}
}


