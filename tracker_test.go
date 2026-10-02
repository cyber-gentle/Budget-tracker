package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestSubscriptionsFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_subs.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "subuser"
	err = store.Signup(username, "sub@spendly.app", "pass123")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	// 1. Add subscriptions
	dueDate1 := time.Now().AddDate(0, 0, 3)
	sub1, err := store.AddSubscription(username, "Netflix", 4500, "entertainment", "monthly", dueDate1)
	if err != nil {
		t.Fatalf("failed to add sub1: %v", err)
	}
	if sub1.Name != "Netflix" || sub1.Amount != 4500 || sub1.BillingCycle != "monthly" {
		t.Fatalf("unexpected sub1: %+v", sub1)
	}

	dueDate2 := time.Now().AddDate(0, 0, 10)
	sub2, err := store.AddSubscription(username, "Gym Membership", 120000, "health", "yearly", dueDate2)
	if err != nil {
		t.Fatalf("failed to add sub2: %v", err)
	}
	if sub2.Name != "Gym Membership" || sub2.Amount != 120000 {
		t.Fatalf("unexpected sub2: %+v", sub2)
	}

	// 2. Get Subscriptions
	subs, err := store.GetSubscriptions(username)
	if err != nil || len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d (err: %v)", len(subs), err)
	}

	// 3. Monthly commitment: 4500 + (120000/12 = 10000) = 14500
	commitment, err := store.GetMonthlyCommitment(username)
	if err != nil {
		t.Fatalf("failed to get commitment: %v", err)
	}
	if commitment < 14499 || commitment > 14501 {
		t.Fatalf("expected monthly commitment ~14500, got %v", commitment)
	}

	// 4. Pay Netflix subscription
	paidSub, err := store.PaySubscription(sub1.ID, username)
	if err != nil {
		t.Fatalf("failed to pay subscription: %v", err)
	}
	// Verify next due date was rolled forward
	if !paidSub.NextDueDate.After(dueDate1) {
		t.Fatalf("expected next due date to advance beyond %v, got %v", dueDate1, paidSub.NextDueDate)
	}

	// Verify expense transaction was recorded
	txs, err := store.GetTransactions(username)
	if err != nil || len(txs) != 1 {
		t.Fatalf("expected 1 expense transaction recorded, got %d", len(txs))
	}
	if txs[0].Amount != 4500 || txs[0].Type != "expense" {
		t.Fatalf("unexpected transaction recorded: %+v", txs[0])
	}

	// 5. Update subscription
	updated, err := store.UpdateSubscription(sub2.ID, username, "Premium Gym", 140000, "health", "yearly", dueDate2, "active")
	if err != nil || updated.Name != "Premium Gym" {
		t.Fatalf("failed to update subscription: %v", err)
	}

	// 6. Delete subscription
	deleted, err := store.DeleteSubscription(sub1.ID, username)
	if err != nil || !deleted {
		t.Fatalf("failed to delete subscription: %v", err)
	}

	subsAfter, _ := store.GetSubscriptions(username)
	if len(subsAfter) != 1 {
		t.Fatalf("expected 1 subscription remaining, got %d", len(subsAfter))
	}
}

func TestGoalsFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_goals.db")
	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to create db store: %v", err)
	}

	username := "goal_user"
	err = store.Signup(username, "goal@spendly.app", "pass123")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	targetDate := time.Now().AddDate(0, 3, 0) // 3 months from now

	// 1. Create Goals
	goal1, err := store.CreateGoal(username, "Emergency Fund", 50000, &targetDate, "🛡️", "#10B981", "emergency")
	if err != nil {
		t.Fatalf("failed to create goal 1: %v", err)
	}
	if goal1.Remaining != 50000 || goal1.Percentage != 0 {
		t.Fatalf("unexpected goal state: %+v", goal1)
	}

	goal2, err := store.CreateGoal(username, "New Laptop", 120000, nil, "💻", "#3B82F6", "tech")
	if err != nil {
		t.Fatalf("failed to create goal 2: %v", err)
	}

	// 2. Get Goals & Summary
	goals, summary, err := store.GetGoals(username)
	if err != nil || len(goals) != 2 {
		t.Fatalf("expected 2 goals, got %d (err: %v)", len(goals), err)
	}
	if summary.TotalTarget != 170000 || summary.TotalSaved != 0 || summary.TotalRemaining != 170000 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// 3. Deposit to Emergency Fund with logTransaction=true
	updatedGoal1, err := store.DepositToGoal(goal1.ID, username, 20000, "Initial savings deposit", true)
	if err != nil {
		t.Fatalf("failed to deposit: %v", err)
	}
	if updatedGoal1.SavedAmount != 20000 || updatedGoal1.Remaining != 30000 || updatedGoal1.Percentage != 40 {
		t.Fatalf("unexpected updated goal: %+v", updatedGoal1)
	}

	// Check expense transaction logged
	txs, err := store.GetTransactions(username)
	if err != nil || len(txs) != 1 {
		t.Fatalf("expected 1 expense transaction logged, got %d", len(txs))
	}
	if txs[0].Amount != 20000 || txs[0].Type != "expense" {
		t.Fatalf("unexpected transaction: %+v", txs[0])
	}

	// 4. Deposit remaining to complete Emergency Fund
	completedGoal1, err := store.DepositToGoal(goal1.ID, username, 30000, "Final top up", false)
	if err != nil {
		t.Fatalf("failed to deposit: %v", err)
	}
	if completedGoal1.Status != "completed" || completedGoal1.Percentage != 100 || completedGoal1.Remaining != 0 {
		t.Fatalf("expected goal to be completed: %+v", completedGoal1)
	}

	// 5. Withdraw partial amount from Emergency Fund
	withdrawnGoal1, err := store.WithdrawFromGoal(goal1.ID, username, 5000, "Emergency car repair", true)
	if err != nil {
		t.Fatalf("failed to withdraw: %v", err)
	}
	if withdrawnGoal1.SavedAmount != 45000 || withdrawnGoal1.Status != "in_progress" {
		t.Fatalf("expected goal to reopen to in_progress with 45000 saved, got: %+v", withdrawnGoal1)
	}

	// Check income transaction logged for withdrawal
	txs, _ = store.GetTransactions(username)
	if len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txs))
	}

	// 6. Test withdrawing more than saved fails
	_, err = store.WithdrawFromGoal(goal1.ID, username, 999999, "Overdraw test", false)
	if err == nil {
		t.Fatalf("expected error when withdrawing more than saved, got nil")
	}

	// 7. Verify contributions ledger
	goalWithLedger, contributions, err := store.GetGoalByID(goal1.ID, username)
	if err != nil {
		t.Fatalf("failed to get goal with ledger: %v", err)
	}
	if goalWithLedger.SavedAmount != 45000 {
		t.Fatalf("expected 45000 saved, got %v", goalWithLedger.SavedAmount)
	}
	if len(contributions) != 3 { // 2 deposits, 1 withdrawal
		t.Fatalf("expected 3 contributions, got %d", len(contributions))
	}

	// 8. Update Goal
	newTargetDate := time.Now().AddDate(0, 6, 0)
	updated2, err := store.UpdateGoal(goal2.ID, username, "MacBook Pro M3", 150000, &newTargetDate, "🍏", "#8B5CF6", "tech", "in_progress")
	if err != nil || updated2.Name != "MacBook Pro M3" || updated2.TargetAmount != 150000 {
		t.Fatalf("failed to update goal 2: %v", err)
	}

	// 9. Delete Goal
	deleted, err := store.DeleteGoal(goal2.ID, username)
	if err != nil || !deleted {
		t.Fatalf("failed to delete goal: %v", err)
	}

	goalsAfter, _, _ := store.GetGoals(username)
	if len(goalsAfter) != 1 {
		t.Fatalf("expected 1 goal remaining, got %d", len(goalsAfter))
	}
}

func TestCategoriesAndTagsFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_categories.db")
	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to create db store: %v", err)
	}

	username := "cat_user"
	err = store.Signup(username, "cat@spendly.app", "pass123")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 1. Initial categories fetch should auto-seed default categories
	cats, err := store.GetCategories(username)
	if err != nil || len(cats) == 0 {
		t.Fatalf("expected default categories to be seeded, got %d (err: %v)", len(cats), err)
	}
	defaultCount := len(cats)

	// 2. Create custom category
	customCat, err := store.CreateCategory(username, "Cryptocurrency & Web3", "income", "🪙", "#F59E0B")
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	if customCat.Slug != "cryptocurrency_and_web3" || customCat.Type != "income" {
		t.Fatalf("unexpected custom category: %+v", customCat)
	}

	// 3. Add transactions with tags
	err = store.AddTransactionWithTags(username, 45000, customCat.Slug, "Ethereum staking reward", "income", "#crypto, #passive, #tax-2026")
	if err != nil {
		t.Fatalf("failed to add transaction with tags: %v", err)
	}

	err = store.AddTransactionWithTags(username, 12000, "food", "Dinner with team", "expense", "team, dining, #work")
	if err != nil {
		t.Fatalf("failed to add second transaction: %v", err)
	}

	// 4. Verify transaction tags parsing
	txs, err := store.GetTransactions(username)
	if err != nil || len(txs) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(txs))
	}
	if len(txs[0].TagList) == 0 && len(txs[1].TagList) == 0 {
		t.Fatalf("expected tags to be parsed into TagList: %+v", txs)
	}

	// 5. Verify popular tags
	popularTags, err := store.GetPopularTags(username)
	if err != nil || len(popularTags) == 0 {
		t.Fatalf("expected popular tags, got %v", popularTags)
	}
	hasCrypto := false
	for _, pt := range popularTags {
		if pt == "crypto" {
			hasCrypto = true
			break
		}
	}
	if !hasCrypto {
		t.Fatalf("expected 'crypto' in popular tags, got %v", popularTags)
	}

	// 6. Verify category transaction counts
	catsAfter, _ := store.GetCategories(username)
	if len(catsAfter) != defaultCount+1 {
		t.Fatalf("expected %d categories, got %d", defaultCount+1, len(catsAfter))
	}
	var foundCustom *app.Category
	for i := range catsAfter {
		if catsAfter[i].ID == customCat.ID {
			foundCustom = &catsAfter[i]
			break
		}
	}
	if foundCustom == nil || foundCustom.TxCount != 1 {
		t.Fatalf("expected custom category to have tx_count=1, got %+v", foundCustom)
	}

	// 7. Update category
	updatedCat, err := store.UpdateCategory(customCat.ID, username, "Crypto & DeFi Staking", "💎", "#8B5CF6")
	if err != nil || updatedCat.Name != "Crypto & DeFi Staking" || updatedCat.Emoji != "💎" {
		t.Fatalf("failed to update category: %v", err)
	}

	// 8. Delete category with transaction reassignment
	deleted, err := store.DeleteCategory(customCat.ID, username, "investments")
	if err != nil || !deleted {
		t.Fatalf("failed to delete category: %v", err)
	}

	// Verify transaction was reassigned to 'investments'
	txsReassigned, _ := store.GetTransactions(username)
	for _, tItem := range txsReassigned {
		if tItem.Amount == 45000 && tItem.Category != "investments" {
			t.Fatalf("expected transaction category to be reassigned to 'investments', got '%s'", tItem.Category)
		}
	}
}

func TestAccountsAndTransfersFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_accounts.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "walletuser"
	_ = store.Signup(username, "wallet@spendly.app", "pass12345", "Wallet Pro")

	// 1. Initial accounts auto-seed
	accounts, err := store.GetAccounts(username)
	if err != nil {
		t.Fatalf("failed to get accounts: %v", err)
	}
	if len(accounts) < 3 {
		t.Fatalf("expected at least 3 seeded accounts, got %d", len(accounts))
	}
	defaultFound := false
	var bankID, momoID int
	for _, a := range accounts {
		if a.IsDefault {
			defaultFound = true
			bankID = a.ID
		}
		if a.Type == "mobile_money" {
			momoID = a.ID
		}
	}
	if !defaultFound || bankID == 0 || momoID == 0 {
		t.Fatalf("expected default bank account and mobile money account to be seeded")
	}

	// 2. Create custom account (Savings Vault) with initial balance
	savingsAcc, err := store.CreateAccount(username, "Emergency Vault", "savings", "₦", "#10B981", "💎", 50000, false)
	if err != nil {
		t.Fatalf("failed to create savings account: %v", err)
	}
	if savingsAcc.CurrentBalance != 50000 {
		t.Fatalf("expected initial balance 50000, got %v", savingsAcc.CurrentBalance)
	}

	// 3. Add transactions tied to specific accounts
	// Income to Bank: +100,000
	err = store.AddTransactionFull(username, 100000, "salary", "Monthly Salary", "income", "#salary", bankID)
	if err != nil {
		t.Fatalf("failed to add income: %v", err)
	}

	// Expense from Mobile Money: -15,000
	err = store.AddTransactionFull(username, 15000, "utilities", "Internet Bill", "expense", "#wifi", momoID)
	if err != nil {
		t.Fatalf("failed to add expense: %v", err)
	}

	// Legacy transaction with account_id=0 (should map to default account bankID)
	err = store.AddTransactionWithTags(username, 5000, "food", "Lunch", "expense", "")
	if err != nil {
		t.Fatalf("failed to add legacy transaction: %v", err)
	}

	// 4. Verify balances
	accountsAfterTx, err := store.GetAccounts(username)
	if err != nil {
		t.Fatalf("failed to get updated accounts: %v", err)
	}
	var curBank, curMoMo, curSavings *app.Account
	for i := range accountsAfterTx {
		if accountsAfterTx[i].ID == bankID {
			curBank = &accountsAfterTx[i]
		} else if accountsAfterTx[i].ID == momoID {
			curMoMo = &accountsAfterTx[i]
		} else if accountsAfterTx[i].ID == savingsAcc.ID {
			curSavings = &accountsAfterTx[i]
		}
	}
	// Bank: 0 (initial) + 100,000 (income) - 5,000 (legacy food) = 95,000
	if curBank.CurrentBalance != 95000 {
		t.Fatalf("expected bank balance 95000, got %v", curBank.CurrentBalance)
	}
	// MoMo: 0 (initial) - 15,000 (expense) = -15,000
	if curMoMo.CurrentBalance != -15000 {
		t.Fatalf("expected momo balance -15000, got %v", curMoMo.CurrentBalance)
	}
	// Savings: 50,000
	if curSavings.CurrentBalance != 50000 {
		t.Fatalf("expected savings balance 50000, got %v", curSavings.CurrentBalance)
	}

	// 5. Transfer funds from Bank to Savings Vault (30,000)
	tr, err := store.CreateAccountTransfer(username, bankID, savingsAcc.ID, 30000, "Fund emergency vault")
	if err != nil {
		t.Fatalf("failed to create transfer: %v", err)
	}
	if tr.Amount != 30000 || tr.FromAccountID != bankID || tr.ToAccountID != savingsAcc.ID {
		t.Fatalf("unexpected transfer result: %+v", tr)
	}

	// Check balances after transfer
	accountsAfterTr, _ := store.GetAccounts(username)
	for i := range accountsAfterTr {
		if accountsAfterTr[i].ID == bankID {
			if accountsAfterTr[i].CurrentBalance != 65000 {
				t.Fatalf("expected bank balance after transfer 65000, got %v", accountsAfterTr[i].CurrentBalance)
			}
		} else if accountsAfterTr[i].ID == savingsAcc.ID {
			if accountsAfterTr[i].CurrentBalance != 80000 {
				t.Fatalf("expected savings balance after transfer 80000, got %v", accountsAfterTr[i].CurrentBalance)
			}
		}
	}

	// 6. Check transfer history
	transfers, err := store.GetAccountTransfers(username, 10)
	if err != nil || len(transfers) != 1 {
		t.Fatalf("expected 1 transfer in history, got %d (err: %v)", len(transfers), err)
	}
	if transfers[0].FromAccountName != "Main Bank" || transfers[0].ToAccountName != "Emergency Vault" {
		t.Fatalf("expected proper joined account names in transfer: %+v", transfers[0])
	}

	// 7. Test invalid transfers
	if _, err := store.CreateAccountTransfer(username, bankID, bankID, 5000, "same"); err == nil {
		t.Fatalf("expected transfer to same account to fail")
	}
	if _, err := store.CreateAccountTransfer(username, bankID, savingsAcc.ID, -100, "neg"); err == nil {
		t.Fatalf("expected negative transfer amount to fail")
	}

	// 8. Update account details
	updatedAcc, err := store.UpdateAccount(savingsAcc.ID, username, "High-Yield Savings", "savings", "₦", "#6366F1", "📈", 60000, false)
	if err != nil || updatedAcc.Name != "High-Yield Savings" || updatedAcc.Icon != "📈" {
		t.Fatalf("failed to update account: %+v (err: %v)", updatedAcc, err)
	}

	// 9. Change default account
	err = store.SetDefaultAccount(savingsAcc.ID, username)
	if err != nil {
		t.Fatalf("failed to set default account: %v", err)
	}
	checkAccs, _ := store.GetAccounts(username)
	for _, a := range checkAccs {
		if a.ID == savingsAcc.ID && !a.IsDefault {
			t.Fatalf("expected savings account to be default now")
		}
		if a.ID == bankID && a.IsDefault {
			t.Fatalf("expected previous default account to no longer be default")
		}
	}

	// 10. Delete account with transaction reassignment
	deleted, err := store.DeleteAccount(momoID, username, bankID)
	if err != nil || !deleted {
		t.Fatalf("failed to delete momo account: %v", err)
	}
	// Verify momo's transaction was reassigned to bankID
	txs, _ := store.GetTransactions(username)
	for _, tx := range txs {
		if tx.Amount == 15000 && tx.AccountID != bankID {
			t.Fatalf("expected transaction to be reassigned to bankID %d, got %d", bankID, tx.AccountID)
		}
	}
}

func TestReportsAndAnalyticsFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_reports.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "analyst_user"
	_ = store.Signup(username, "analyst@example.com", "hash123", "Analyst User")

	// 1. Setup accounts
	bank, err := store.CreateAccount(username, "Zenith Bank", "bank", "₦", "#3B82F6", "🏦", 150000, true)
	if err != nil {
		t.Fatalf("failed to create bank account: %v", err)
	}
	cash, err := store.CreateAccount(username, "Cash Wallet", "cash", "₦", "#10B981", "💵", 20000, false)
	if err != nil {
		t.Fatalf("failed to create cash account: %v", err)
	}

	// 2. Add transactions across time
	now := time.Now()
	thisMonthDate := time.Date(now.Year(), now.Month(), 5, 12, 0, 0, 0, now.Location())
	lastMonthDate := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, now.Location()).AddDate(0, -1, 4)

	// Income this month (Bank)
	err = store.AddTransactionFull(username, 300000, "salary", "Monthly Tech Salary", "income", "salary, work", bank.ID, thisMonthDate)
	if err != nil {
		t.Fatalf("failed to add income: %v", err)
	}

	// Expenses this month
	_ = store.AddTransactionFull(username, 50000, "housing", "Apartment Rent", "expense", "rent, housing", bank.ID, thisMonthDate)
	_ = store.AddTransactionFull(username, 20000, "food", "Supermarket Groceries", "expense", "food, groceries", bank.ID, thisMonthDate)
	_ = store.AddTransactionFull(username, 10000, "transport", "Fuel & Uber", "expense", "fuel, travel", cash.ID, thisMonthDate)

	// Expense last month
	_ = store.AddTransactionFull(username, 25000, "entertainment", "Concert Tickets", "expense", "fun, music", bank.ID, lastMonthDate)

	// 3. Test This Month Report
	rep, err := store.GetDetailedFinancialReport(username, "this_month", "", "", 0, "")
	if err != nil {
		t.Fatalf("failed to get report: %v", err)
	}

	if rep.KPIs.TotalIncome != 300000 {
		t.Fatalf("expected total income 300000, got %.2f", rep.KPIs.TotalIncome)
	}
	if rep.KPIs.TotalExpense != 80000 {
		t.Fatalf("expected total expense 80000, got %.2f", rep.KPIs.TotalExpense)
	}
	if rep.KPIs.NetSavings != 220000 {
		t.Fatalf("expected net savings 220000, got %.2f", rep.KPIs.NetSavings)
	}
	if rep.KPIs.SavingsRate != 73 {
		t.Fatalf("expected savings rate 73%%, got %d%%", rep.KPIs.SavingsRate)
	}
	if rep.KPIs.LargestExpenseAmount != 50000 {
		t.Fatalf("expected largest expense 50000, got %.2f", rep.KPIs.LargestExpenseAmount)
	}
	if rep.KPIs.EmergencyRunwayMonths <= 0 {
		t.Fatalf("expected positive emergency runway months, got %.2f", rep.KPIs.EmergencyRunwayMonths)
	}

	// Verify Category breakdown
	if len(rep.ExpenseCategories) != 3 {
		t.Fatalf("expected 3 expense categories, got %d", len(rep.ExpenseCategories))
	}
	if rep.ExpenseCategories[0].Amount != 50000 || rep.ExpenseCategories[0].Category != "housing" {
		t.Fatalf("expected top category to be housing (50000), got %v", rep.ExpenseCategories[0])
	}

	// Verify Tag breakdown
	if len(rep.TagsAnalytics) == 0 {
		t.Fatalf("expected tags analytics to be populated")
	}

	// Verify Account distribution
	if len(rep.AccountDistribution) != 2 {
		t.Fatalf("expected 2 active accounts in distribution, got %d", len(rep.AccountDistribution))
	}

	// 4. Test Last Month Report
	lastMonthRep, err := store.GetDetailedFinancialReport(username, "last_month", "", "", 0, "")
	if err != nil {
		t.Fatalf("failed to get last month report: %v", err)
	}
	if lastMonthRep.KPIs.TotalExpense != 25000 {
		t.Fatalf("expected last month expense 25000, got %.2f", lastMonthRep.KPIs.TotalExpense)
	}

	// 5. Test Filter by Account (Cash only)
	cashRep, err := store.GetDetailedFinancialReport(username, "this_month", "", "", cash.ID, "")
	if err != nil {
		t.Fatalf("failed to get cash report: %v", err)
	}
	if cashRep.KPIs.TotalExpense != 10000 {
		t.Fatalf("expected cash only expense to be 10000, got %.2f", cashRep.KPIs.TotalExpense)
	}

	// 6. Test Filter by Category (Food only)
	foodRep, err := store.GetDetailedFinancialReport(username, "this_month", "", "", 0, "food")
	if err != nil {
		t.Fatalf("failed to get food report: %v", err)
	}
	if foodRep.KPIs.TotalExpense != 20000 {
		t.Fatalf("expected food only expense to be 20000, got %.2f", foodRep.KPIs.TotalExpense)
	}

	// 7. Test HTTP API Endpoints
	application := app.NewApp(store)
	sessionID, err := store.Login("analyst_user", "hash123")
	if err != nil || sessionID == "" {
		t.Fatalf("login failed: %v", err)
	}

	// A. GET /api/reports
	req := httptest.NewRequest(http.MethodGet, "/api/reports?timeframe=this_month", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	rec := httptest.NewRecorder()
	application.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/reports, got %d: %s", rec.Code, rec.Body.String())
	}
	var apiReport app.FinancialReport
	if err := json.Unmarshal(rec.Body.Bytes(), &apiReport); err != nil {
		t.Fatalf("failed to decode /api/reports response: %v", err)
	}
	if apiReport.KPIs.TotalIncome != 300000 {
		t.Fatalf("expected API report income 300000, got %.2f", apiReport.KPIs.TotalIncome)
	}

	// B. GET /api/export (CSV)
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/export?timeframe=this_month", nil)
	reqCSV.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recCSV := httptest.NewRecorder()
	application.ServeHTTP(recCSV, reqCSV)

	if recCSV.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/export, got %d", recCSV.Code)
	}
	csvBody := recCSV.Body.String()
	if !strings.Contains(csvBody, "Zenith Bank") || !strings.Contains(csvBody, "Monthly Tech Salary") {
		t.Fatalf("expected CSV export to contain account name and transaction details, got: %s", csvBody)
	}

	// C. GET /api/export?format=json
	reqJSON := httptest.NewRequest(http.MethodGet, "/api/export?timeframe=this_month&format=json", nil)
	reqJSON.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recJSON := httptest.NewRecorder()
	application.ServeHTTP(recJSON, reqJSON)

	if recJSON.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/export?format=json, got %d", recJSON.Code)
	}
	var jsonExport []app.Transaction
	if err := json.Unmarshal(recJSON.Body.Bytes(), &jsonExport); err != nil {
		t.Fatalf("failed to parse JSON export: %v", err)
	}
	if len(jsonExport) != 4 {
		t.Fatalf("expected 4 transactions in export for this month, got %d", len(jsonExport))
	}
}





