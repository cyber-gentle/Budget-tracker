package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSmartBudgetingRulesFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "test_smart_budgets.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "smartbudgeter"
	_ = store.Signup(username, "smart@spendly.app", "pass12345", "Smart Budgeter")

	// 1. Test Category Bucket Classification
	if app.ClassifyCategoryBucket("food") != "needs" {
		t.Fatalf("expected food to be 'needs', got '%s'", app.ClassifyCategoryBucket("food"))
	}
	if app.ClassifyCategoryBucket("housing") != "needs" {
		t.Fatalf("expected housing to be 'needs'")
	}
	if app.ClassifyCategoryBucket("entertainment") != "wants" {
		t.Fatalf("expected entertainment to be 'wants'")
	}
	if app.ClassifyCategoryBucket("shopping") != "wants" {
		t.Fatalf("expected shopping to be 'wants'")
	}
	if app.ClassifyCategoryBucket("savings") != "savings" {
		t.Fatalf("expected savings to be 'savings'")
	}
	if app.ClassifyCategoryBucket("crypto_vault") != "savings" {
		t.Fatalf("expected crypto_vault to be 'savings'")
	}
	if app.ClassifyCategoryBucket("pharmacy_bills") != "needs" {
		t.Fatalf("expected pharmacy_bills to be 'needs'")
	}

	// 2. Add Transactions for current month
	now := time.Now()
	// Income: ₦400,000
	_ = store.AddTransactionFull(username, 400000, "salary", "Main Salary", "income", "#work", 1, now)

	// Needs (50% target = ₦200,000): Food ₦60,000 + Housing ₦100,000 + Transport ₦20,000 = ₦180,000
	_ = store.AddTransactionFull(username, 60000, "food", "Groceries & Market", "expense", "#home", 1, now)
	_ = store.AddTransactionFull(username, 100000, "housing", "Apartment Rent Share", "expense", "#fixed", 1, now)
	_ = store.AddTransactionFull(username, 20000, "transport", "Fuel & Transit", "expense", "#commute", 1, now)

	// Wants (30% target = ₦120,000): Entertainment ₦50,000 + Shopping ₦30,000 = ₦80,000
	_ = store.AddTransactionFull(username, 50000, "entertainment", "Weekend Outing", "expense", "#fun", 1, now)
	_ = store.AddTransactionFull(username, 30000, "shopping", "New Sneakers", "expense", "#fashion", 1, now)

	// Savings (20% target = ₦80,000): Savings transaction ₦40,000
	_ = store.AddTransactionFull(username, 40000, "savings", "Emergency Fund Deposit", "expense", "#vault", 1, now)

	// Set custom category envelope limits
	_ = store.SetBudget(username, "food", 50000)          // Spent 60,000 -> Exceeded!
	_ = store.SetBudget(username, "entertainment", 60000) // Spent 50,000 / 60,000 -> 83% (Caution/Near limit)
	_ = store.SetBudget(username, "transport", 40000)     // Spent 20,000 / 40,000 -> 50% (On track)

	// 3. Test GetSmartBudgetReport
	report, err := store.GetSmartBudgetReport(username)
	if err != nil {
		t.Fatalf("failed to get smart budget report: %v", err)
	}

	if report.MonthlyIncome != 400000 {
		t.Fatalf("expected monthly income 400000, got %.2f", report.MonthlyIncome)
	}
	if report.TotalExpense != 300000 {
		t.Fatalf("expected total expense 300000, got %.2f", report.TotalExpense)
	}

	// Verify 50/30/20 Buckets
	if len(report.Rule503020) != 3 {
		t.Fatalf("expected 3 buckets in 50/30/20 rule, got %d", len(report.Rule503020))
	}

	var needsBucket, wantsBucket, savingsBucket *app.Rule503020Bucket
	for i := range report.Rule503020 {
		switch report.Rule503020[i].Key {
		case "needs":
			needsBucket = &report.Rule503020[i]
		case "wants":
			wantsBucket = &report.Rule503020[i]
		case "savings":
			savingsBucket = &report.Rule503020[i]
		}
	}

	if needsBucket == nil || needsBucket.TargetAmount != 200000 || needsBucket.ActualSpent != 180000 {
		t.Fatalf("unexpected needs bucket: %+v", needsBucket)
	}
	if wantsBucket == nil || wantsBucket.TargetAmount != 120000 || wantsBucket.ActualSpent != 80000 {
		t.Fatalf("unexpected wants bucket: %+v", wantsBucket)
	}
	// Total savings includes explicit savings (40,000) + surplus income (400,000 - 300,000 = 100,000) = 140,000
	if savingsBucket == nil || savingsBucket.TargetAmount != 80000 || savingsBucket.ActualSpent != 140000 {
		t.Fatalf("unexpected savings bucket: %+v", savingsBucket)
	}

	// Verify Envelopes
	var foodEnv, entEnv, transEnv *app.BudgetEnvelope
	for i := range report.Envelopes {
		switch report.Envelopes[i].Category {
		case "food":
			foodEnv = &report.Envelopes[i]
		case "entertainment":
			entEnv = &report.Envelopes[i]
		case "transport":
			transEnv = &report.Envelopes[i]
		}
	}

	if foodEnv == nil || foodEnv.PaceStatus != "exceeded" {
		t.Fatalf("expected food envelope to be 'exceeded', got %+v", foodEnv)
	}
	if entEnv == nil || (entEnv.PaceStatus != "caution" && entEnv.PaceStatus != "warning") {
		t.Fatalf("expected entertainment envelope to be caution or warning, got %+v", entEnv)
	}
	if transEnv == nil {
		t.Fatalf("expected transport envelope to exist")
	}

	// Verify Dynamic Alerts
	if len(report.Alerts) == 0 {
		t.Fatalf("expected spending alerts to be generated")
	}
	hasExceededAlert := false
	for _, a := range report.Alerts {
		if a.Category == "food" && a.Type == "danger" {
			hasExceededAlert = true
			break
		}
	}
	if !hasExceededAlert {
		t.Fatalf("expected danger alert for exceeded food envelope, got: %+v", report.Alerts)
	}

	// 4. Test Apply503020AutoBudget
	autoBudgets, err := store.Apply503020AutoBudget(username, 500000)
	if err != nil {
		t.Fatalf("failed to apply auto 50/30/20 budget: %v", err)
	}

	// Needs (50% of 500k = 250k): food (40% of needs = 100k), housing (35% = 87.5k), transport (15% = 37.5k), bills (10% = 25k)
	if autoBudgets["food"] != 100000 {
		t.Fatalf("expected auto food budget 100000, got %.2f", autoBudgets["food"])
	}
	if autoBudgets["savings"] != 100000 { // 20% of 500k
		t.Fatalf("expected auto savings budget 100000, got %.2f", autoBudgets["savings"])
	}

	// 5. Test HTTP API Endpoints
	application := app.NewApp(store)
	sessionID, err := store.Login(username, "pass12345")
	if err != nil || sessionID == "" {
		t.Fatalf("failed to login: %v", err)
	}

	// A. GET /api/budgets/smart
	reqSmart := httptest.NewRequest(http.MethodGet, "/api/budgets/smart", nil)
	reqSmart.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recSmart := httptest.NewRecorder()
	application.ServeHTTP(recSmart, reqSmart)

	if recSmart.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/budgets/smart, got %d: %s", recSmart.Code, recSmart.Body.String())
	}
	var apiSmartReport app.SmartBudgetReport
	if err := json.Unmarshal(recSmart.Body.Bytes(), &apiSmartReport); err != nil {
		t.Fatalf("failed to parse /api/budgets/smart response: %v", err)
	}
	if len(apiSmartReport.Rule503020) != 3 {
		t.Fatalf("expected 3 50/30/20 rules in API response, got %d", len(apiSmartReport.Rule503020))
	}

	// B. POST /api/budgets/auto-503020
	form := url.Values{}
	form.Set("base_income", "600000")
	reqAuto := httptest.NewRequest(http.MethodPost, "/api/budgets/auto-503020", strings.NewReader(form.Encode()))
	reqAuto.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAuto.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recAuto := httptest.NewRecorder()
	application.ServeHTTP(recAuto, reqAuto)

	if recAuto.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/budgets/auto-503020, got %d: %s", recAuto.Code, recAuto.Body.String())
	}
	var autoResp map[string]any
	if err := json.Unmarshal(recAuto.Body.Bytes(), &autoResp); err != nil {
		t.Fatalf("failed to unmarshal auto budget response: %v", err)
	}
	if autoResp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", autoResp["status"])
	}

	// C. GET /api/budgets includes smart_report
	reqBudgets := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	reqBudgets.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recBudgets := httptest.NewRecorder()
	application.ServeHTTP(recBudgets, reqBudgets)

	if recBudgets.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/budgets, got %d", recBudgets.Code)
	}
	var budgetsResp map[string]any
	if err := json.Unmarshal(recBudgets.Body.Bytes(), &budgetsResp); err != nil {
		t.Fatalf("failed to unmarshal /api/budgets response: %v", err)
	}
	if budgetsResp["smart_report"] == nil {
		t.Fatalf("expected /api/budgets to contain 'smart_report'")
	}
}

func TestSplitExpensesAndSettlementFlow(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test_splits.db")
	store, err := app.NewDBStore(tempDB, "")
	if err != nil {
		t.Fatalf("failed to create db store: %v", err)
	}

	username := "splitter_user"
	err = store.Signup(username, "splitter@test.com", "pass123", "Split Master")
	if err != nil {
		t.Fatalf("failed to signup: %v", err)
	}

	// 1. Create a split expense: Dinner at Bistro, Total: 90000, 3 participants (You: 30000, Alice: 30000, Bob: 30000)
	parts := []app.SplitParticipantInput{
		{Name: "You", ShareAmount: 30000, IsUser: true},
		{Name: "Alice", ShareAmount: 30000, IsUser: false},
		{Name: "Bob", ShareAmount: 30000, IsUser: false},
	}
	split, err := store.CreateSplitExpense(
		username, "Team Dinner at Bistro", "Food", "Celebration dinner",
		90000, time.Now(), "You", true, "equal", parts, true, 0,
	)
	if err != nil {
		t.Fatalf("failed to create split expense: %v", err)
	}

	if split.ID <= 0 || split.TotalAmount != 90000 {
		t.Fatalf("unexpected split: %+v", split)
	}
	if len(split.Participants) != 3 {
		t.Fatalf("expected 3 participants, got %d", len(split.Participants))
	}
	if split.UserShare != 30000 {
		t.Fatalf("expected user share 30000, got %.2f", split.UserShare)
	}

	// Verify automated IOU creation in debts table
	debts, summary, err := store.GetDebts(username)
	if err != nil {
		t.Fatalf("failed to get debts: %v", err)
	}
	if len(debts) != 2 {
		t.Fatalf("expected 2 active IOUs created for Alice and Bob, got %d", len(debts))
	}
	if summary.TotalOwedToUser != 60000 {
		t.Fatalf("expected total owed to user 60000, got %.2f", summary.TotalOwedToUser)
	}

	// 2. Add a debt where User owes Alice 10,000 for Movie Tickets
	_, err = store.CreateDebt(username, "Alice", "i_owe", 10000, nil, "Movie tickets")
	if err != nil {
		t.Fatalf("failed to create debt user owes Alice: %v", err)
	}

	// 3. Test Settlement Overview Calculation
	overview, err := store.GetSettlementOverview(username)
	if err != nil {
		t.Fatalf("failed to get settlement overview: %v", err)
	}
	if overview.ContactsCount != 2 {
		t.Fatalf("expected 2 contacts with open balances (Alice, Bob), got %d", overview.ContactsCount)
	}

	var aliceContact *app.ContactSettlement
	for _, c := range overview.Contacts {
		if strings.EqualFold(c.ContactName, "Alice") {
			aliceContact = &c
			break
		}
	}
	if aliceContact == nil {
		t.Fatalf("expected Alice in settlement overview")
	}
	if aliceContact.TheyOweYou != 30000 || aliceContact.YouOweThem != 10000 {
		t.Fatalf("expected Alice they_owe=30000, you_owe=10000, got they_owe=%.2f, you_owe=%.2f", aliceContact.TheyOweYou, aliceContact.YouOweThem)
	}
	if aliceContact.NetAmount != 20000 || aliceContact.Status != "they_owe" {
		t.Fatalf("expected Alice net 20000 (they_owe), got %.2f (%s)", aliceContact.NetAmount, aliceContact.Status)
	}

	// 4. Test Mutual Debt Simplification (offset_only)
	offsetRes, err := store.SettleContactDebts(username, "Alice", "offset_only", 0, false)
	if err != nil {
		t.Fatalf("failed to offset debts with Alice: %v", err)
	}
	if offsetRes.OffsetAmount != 10000 {
		t.Fatalf("expected offset 10000, got %.2f", offsetRes.OffsetAmount)
	}

	// After offset, Alice should now owe 20,000 net, and User owes Alice 0
	overviewAfterOffset, err := store.GetSettlementOverview(username)
	if err != nil {
		t.Fatalf("failed to get overview after offset: %v", err)
	}
	for _, c := range overviewAfterOffset.Contacts {
		if strings.EqualFold(c.ContactName, "Alice") {
			if c.TheyOweYou != 20000 || c.YouOweThem != 0 || c.NetAmount != 20000 {
				t.Fatalf("after offset, expected Alice they_owe=20000, you_owe=0, got they_owe=%.2f, you_owe=%.2f", c.TheyOweYou, c.YouOweThem)
			}
		}
	}

	// 5. Test Full Settlement with transaction recording
	settleRes, err := store.SettleContactDebts(username, "Alice", "full", 0, true)
	if err != nil {
		t.Fatalf("failed to full settle with Alice: %v", err)
	}
	if settleRes.NetSettled != 20000 {
		t.Fatalf("expected net settled 20000, got %.2f", settleRes.NetSettled)
	}

	// Verify all debts with Alice are settled
	overviewAfterSettle, err := store.GetSettlementOverview(username)
	if err != nil {
		t.Fatalf("failed to get overview after settle: %v", err)
	}
	for _, c := range overviewAfterSettle.Contacts {
		if strings.EqualFold(c.ContactName, "Alice") {
			t.Fatalf("Alice should have no remaining active debts, but found: %+v", c)
		}
	}

	// 6. Test HTTP APIs
	application := app.NewApp(store)
	sessionID, err := store.Login(username, "pass123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// A. GET /api/splits
	reqSplits := httptest.NewRequest(http.MethodGet, "/api/splits", nil)
	reqSplits.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recSplits := httptest.NewRecorder()
	application.ServeHTTP(recSplits, reqSplits)
	if recSplits.Code != http.StatusOK {
		t.Fatalf("expected 200 from GET /api/splits, got %d", recSplits.Code)
	}

	// B. POST /api/splits
	newSplitJSON := `{
		"title": "Groceries Run",
		"total_amount": 50000,
		"payer_name": "You",
		"payer_is_user": true,
		"category": "Groceries",
		"split_type": "equal",
		"participants": [
			{"name": "You", "share_amount": 25000, "is_user": true},
			{"name": "Charlie", "share_amount": 25000, "is_user": false}
		]
	}`
	reqPostSplit := httptest.NewRequest(http.MethodPost, "/api/splits", strings.NewReader(newSplitJSON))
	reqPostSplit.Header.Set("Content-Type", "application/json")
	reqPostSplit.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPostSplit := httptest.NewRecorder()
	application.ServeHTTP(recPostSplit, reqPostSplit)
	if recPostSplit.Code != http.StatusCreated {
		t.Fatalf("expected 201 from POST /api/splits, got %d: %s", recPostSplit.Code, recPostSplit.Body.String())
	}

	var createdSplit app.SplitExpense
	if err := json.Unmarshal(recPostSplit.Body.Bytes(), &createdSplit); err != nil {
		t.Fatalf("failed to decode created split: %v", err)
	}

	// C. GET /api/settlements
	reqSettlements := httptest.NewRequest(http.MethodGet, "/api/settlements", nil)
	reqSettlements.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recSettlements := httptest.NewRecorder()
	application.ServeHTTP(recSettlements, reqSettlements)
	if recSettlements.Code != http.StatusOK {
		t.Fatalf("expected 200 from GET /api/settlements, got %d", recSettlements.Code)
	}

	// D. DELETE /api/splits/{id}
	reqDel := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/splits/%d", createdSplit.ID), nil)
	reqDel.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recDel := httptest.NewRecorder()
	application.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("expected 200 from DELETE /api/splits/%d, got %d", createdSplit.ID, recDel.Code)
	}
}

func TestNetWorthAndWealthFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "wealth_test.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "wealthbuilder"
	err = store.Signup(username, "wealth@spendly.app", "pass1234", "Wealth Builder")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	sessionID, err := store.Login(username, "pass1234")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	application := app.NewApp(store)

	// 1. Add baseline assets:
	// A. Accounts / Wallets
	_, err = store.CreateAccount(username, "High Yield Savings", "savings", "USD", "#2563eb", "🏦", 20000, false)
	if err != nil {
		t.Fatalf("failed to add account: %v", err)
	}

	// B. Savings Goal
	goal, err := store.CreateGoal(username, "Home Down Payment", 50000, nil, "🏠", "#10b981", "Savings")
	if err != nil {
		t.Fatalf("failed to add goal: %v", err)
	}
	_, err = store.DepositToGoal(goal.ID, username, 10000, "Initial deposit", false)
	if err != nil {
		t.Fatalf("failed to deposit to goal: %v", err)
	}

	// C. Debts: Money someone owes the user (Asset / IOU)
	_, err = store.CreateDebt(username, "Dave", "owing_me", 3000, nil, "Personal loan")
	if err != nil {
		t.Fatalf("failed to add owing_me debt: %v", err)
	}

	// 2. Add baseline liabilities:
	// A. Debts user owes (Liability)
	_, err = store.CreateDebt(username, "Credit Card Corp", "i_owe", 4000, nil, "Card balance")
	if err != nil {
		t.Fatalf("failed to add i_owe debt: %v", err)
	}

	// B. Subscription monthly cost (Liability)
	subDue := time.Now().AddDate(0, 0, 14)
	_, err = store.AddSubscription(username, "Streaming Bundle", 50, "Entertainment", "monthly", subDue)
	if err != nil {
		t.Fatalf("failed to add subscription: %v", err)
	}

	// 3. Check Initial Net Worth Overview via DB
	overview, err := store.GetNetWorthOverview(username)
	if err != nil {
		t.Fatalf("failed to get net worth overview: %v", err)
	}

	// Expected assets: 20000 (account) + 10000 (goal) + 3000 (debt owed to user) = 33000
	if overview.TotalAssets != 33000 {
		t.Fatalf("expected total assets 33000, got %.2f", overview.TotalAssets)
	}
	// Expected liabilities: 4000 (debt user owes) + 50 (monthly sub) = 4050
	if overview.TotalLiabilities != 4050 {
		t.Fatalf("expected total liabilities 4050, got %.2f", overview.TotalLiabilities)
	}
	// Expected net worth: 33000 - 4050 = 28950
	if overview.NetWorth != 28950 {
		t.Fatalf("expected net worth 28950, got %.2f", overview.NetWorth)
	}
	if overview.SolvencyStatus != "Solvent" {
		t.Fatalf("expected SolvencyStatus 'Solvent', got %q", overview.SolvencyStatus)
	}
	if overview.HealthScore <= 0 || overview.HealthScore > 100 {
		t.Fatalf("expected health score between 1 and 100, got %d", overview.HealthScore)
	}
	if len(overview.Trend) == 0 {
		t.Fatalf("expected non-empty trend points")
	}

	// 4. Test HTTP Page Endpoint GET /net-worth
	reqPage := httptest.NewRequest(http.MethodGet, "/net-worth", nil)
	reqPage.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPage := httptest.NewRecorder()
	application.ServeHTTP(recPage, reqPage)
	if recPage.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /net-worth, got %d", recPage.Code)
	}
	bodyPage := recPage.Body.String()
	if !strings.Contains(bodyPage, "Net Worth") || !strings.Contains(bodyPage, "Financial Health") {
		t.Fatalf("page body missing expected keywords: %s", bodyPage[:500])
	}

	// 5. Test HTTP API GET /api/net-worth
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/net-worth", nil)
	reqAPI.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recAPI := httptest.NewRecorder()
	application.ServeHTTP(recAPI, reqAPI)
	if recAPI.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/net-worth, got %d: %s", recAPI.Code, recAPI.Body.String())
	}
	var apiOverview app.NetWorthOverview
	if err := json.Unmarshal(recAPI.Body.Bytes(), &apiOverview); err != nil {
		t.Fatalf("failed to decode GET /api/net-worth response: %v", err)
	}
	if apiOverview.NetWorth != 28950 {
		t.Fatalf("expected API net worth 28950, got %.2f", apiOverview.NetWorth)
	}

	// 6. Test POST /api/net-worth/items (Create custom asset)
	assetJSON := `{
		"category": "asset",
		"asset_type": "investment",
		"name": "Index Fund ETF",
		"amount": 15000,
		"notes": "S&P 500 Index"
	}`
	reqPostAsset := httptest.NewRequest(http.MethodPost, "/api/net-worth/items", strings.NewReader(assetJSON))
	reqPostAsset.Header.Set("Content-Type", "application/json")
	reqPostAsset.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPostAsset := httptest.NewRecorder()
	application.ServeHTTP(recPostAsset, reqPostAsset)
	if recPostAsset.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /api/net-worth/items, got %d: %s", recPostAsset.Code, recPostAsset.Body.String())
	}

	var createdAssetRes struct {
		Success bool                    `json:"success"`
		Item    app.CustomAssetLiability `json:"item"`
	}
	if err := json.Unmarshal(recPostAsset.Body.Bytes(), &createdAssetRes); err != nil {
		t.Fatalf("failed to parse created asset: %v", err)
	}
	createdAsset := createdAssetRes.Item
	if createdAsset.ID == 0 || createdAsset.Amount != 15000 {
		t.Fatalf("unexpected created asset: %+v", createdAsset)
	}

	// 7. Test POST /api/net-worth/items (Create custom liability)
	liabJSON := `{
		"category": "liability",
		"asset_type": "other",
		"name": "Tax Assessment",
		"amount": 2500,
		"notes": "Estimated Q4 tax"
	}`
	reqPostLiab := httptest.NewRequest(http.MethodPost, "/api/net-worth/items", strings.NewReader(liabJSON))
	reqPostLiab.Header.Set("Content-Type", "application/json")
	reqPostLiab.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPostLiab := httptest.NewRecorder()
	application.ServeHTTP(recPostLiab, reqPostLiab)
	if recPostLiab.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /api/net-worth/items (liab), got %d: %s", recPostLiab.Code, recPostLiab.Body.String())
	}
	var createdLiabRes struct {
		Success bool                    `json:"success"`
		Item    app.CustomAssetLiability `json:"item"`
	}
	if err := json.Unmarshal(recPostLiab.Body.Bytes(), &createdLiabRes); err != nil {
		t.Fatalf("failed to parse created liability: %v", err)
	}
	createdLiab := createdLiabRes.Item

	// 8. Test PUT /api/net-worth/items/{id} (Update asset)
	updateAssetJSON := fmt.Sprintf(`{
		"category": "asset",
		"asset_type": "investment",
		"name": "Index Fund ETF",
		"amount": 18000,
		"notes": "S&P 500 Index + Dividends"
	}`)
	reqPut := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/net-worth/items/%d", createdAsset.ID), strings.NewReader(updateAssetJSON))
	reqPut.Header.Set("Content-Type", "application/json")
	reqPut.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPut := httptest.NewRecorder()
	application.ServeHTTP(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from PUT /api/net-worth/items/%d, got %d: %s", createdAsset.ID, recPut.Code, recPut.Body.String())
	}

	// 9. Test DELETE /api/net-worth/items/{id} (Delete liability)
	reqDeleteLiab := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/net-worth/items/%d", createdLiab.ID), nil)
	reqDeleteLiab.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recDeleteLiab := httptest.NewRecorder()
	application.ServeHTTP(recDeleteLiab, reqDeleteLiab)
	if recDeleteLiab.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from DELETE /api/net-worth/items/%d, got %d: %s", createdLiab.ID, recDeleteLiab.Code, recDeleteLiab.Body.String())
	}

	// 10. Re-verify Net Worth after addition of $18,000 custom asset and removal of liability
	// Total assets = 33000 + 18000 = 51000
	// Total liabilities = 4050
	// Net Worth = 51000 - 4050 = 46950
	finalOverview, err := store.GetNetWorthOverview(username)
	if err != nil {
		t.Fatalf("failed to get final overview: %v", err)
	}
	if finalOverview.TotalAssets != 51000 {
		t.Fatalf("expected 51000 total assets, got %.2f", finalOverview.TotalAssets)
	}
	if finalOverview.TotalLiabilities != 4050 {
		t.Fatalf("expected 4050 total liabilities, got %.2f", finalOverview.TotalLiabilities)
	}
	if finalOverview.NetWorth != 46950 {
		t.Fatalf("expected 46950 net worth, got %.2f", finalOverview.NetWorth)
	}
}

func TestReceiptOCRAndDocumentScannerFlow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := "file:" + filepath.Join(tmpDir, "receipt_test.db")

	store, err := app.NewDBStore(dbPath, "")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	username := "scanneruser"
	err = store.Signup(username, "scanner@spendly.app", "pass1234", "Receipt Scanner User")
	if err != nil {
		t.Fatalf("signup failed: %v", err)
	}

	sessionID, err := store.Login(username, "pass1234")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	application := app.NewApp(store)

	// 1. Test ParseReceiptText Engine
	sampleStarbucks := `STARBUCKS STORE #14920
104 BROADWAY ST
DATE: 10/02/2026 08:42 AM
1 ICED CARAMEL MACCHIATO $6.45
1 BACON GOUDA SANDWICH $5.95
SUBTOTAL $12.40
TAX $1.02
TIP $1.43
TOTAL $14.85
THANK YOU FOR VISITING!`

	parsed1 := app.ParseReceiptText(sampleStarbucks)
	if parsed1.Merchant != "Starbucks" {
		t.Fatalf("expected merchant 'Starbucks', got %q", parsed1.Merchant)
	}
	if parsed1.TotalAmount != 14.85 {
		t.Fatalf("expected total amount 14.85, got %.2f", parsed1.TotalAmount)
	}
	if parsed1.TaxAmount != 1.02 {
		t.Fatalf("expected tax 1.02, got %.2f", parsed1.TaxAmount)
	}
	if parsed1.TipAmount != 1.43 {
		t.Fatalf("expected tip 1.43, got %.2f", parsed1.TipAmount)
	}
	if parsed1.SuggestedCategory != "food" {
		t.Fatalf("expected category 'food', got %q", parsed1.SuggestedCategory)
	}

	// Test Supermarket Receipt
	sampleGrocery := `WHOLE FOODS MARKET #1029
ORGANIC BANANAS $2.49
ALMOND MILK $4.99
WILD SOCKEYE SALMON $24.50
SUBTOTAL $31.98
TOTAL DUE: $31.98
DATE: 2026-10-01`
	parsed2 := app.ParseReceiptText(sampleGrocery)
	if parsed2.Merchant != "Whole Foods" {
		t.Fatalf("expected merchant 'Whole Foods', got %q", parsed2.Merchant)
	}
	if parsed2.TotalAmount != 31.98 {
		t.Fatalf("expected total amount 31.98, got %.2f", parsed2.TotalAmount)
	}
	if parsed2.SuggestedCategory != "groceries" {
		t.Fatalf("expected category 'groceries', got %q", parsed2.SuggestedCategory)
	}

	// 2. Test DB CreateReceipt and GetReceipts
	recDate := time.Date(2026, 10, 2, 8, 42, 0, 0, time.UTC)
	receipt, err := store.CreateReceipt(
		username,
		"/uploads/receipts/sample_starbucks.jpg",
		"sample_starbucks.jpg",
		"Starbucks Coffee",
		14.85,
		1.02,
		1.43,
		&recDate,
		"food",
		sampleStarbucks,
	)
	if err != nil || receipt.ID == 0 {
		t.Fatalf("failed to create receipt: %v", err)
	}

	receiptsList, summary, err := store.GetReceipts(username)
	if err != nil {
		t.Fatalf("failed to get receipts: %v", err)
	}
	if summary.TotalCount != 1 || summary.TotalAmount != 14.85 || summary.UnlinkedCount != 1 {
		t.Fatalf("unexpected receipt summary: %+v", summary)
	}
	if len(receiptsList) != 1 || receiptsList[0].Merchant != "Starbucks Coffee" {
		t.Fatalf("unexpected receipts list: %+v", receiptsList)
	}

	// 3. Test HTTP Page GET /receipts
	reqPage := httptest.NewRequest(http.MethodGet, "/receipts", nil)
	reqPage.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recPage := httptest.NewRecorder()
	application.ServeHTTP(recPage, reqPage)
	if recPage.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /receipts, got %d", recPage.Code)
	}
	if !strings.Contains(recPage.Body.String(), "Receipt & Document OCR Scanner") {
		t.Fatalf("page body missing expected heading")
	}

	// 4. Test HTTP API GET /api/receipts
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/receipts", nil)
	reqAPI.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recAPI := httptest.NewRecorder()
	application.ServeHTTP(recAPI, reqAPI)
	if recAPI.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/receipts, got %d", recAPI.Code)
	}

	// 5. Test HTTP API POST /api/receipts/parse-text
	reqParse := httptest.NewRequest(http.MethodPost, "/api/receipts/parse-text", strings.NewReader(`{"text":"SHELL OIL\nPUMP 04 $45.00\nTOTAL: $45.00"}`))
	reqParse.Header.Set("Content-Type", "application/json")
	reqParse.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recParse := httptest.NewRecorder()
	application.ServeHTTP(recParse, reqParse)
	if recParse.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/receipts/parse-text, got %d", recParse.Code)
	}

	// 6. Test HTTP API POST /api/receipts/scan (Upload/Scan JSON payload)
	scanJSON := `{
		"image_base64": "data:image/svg+xml;utf8,<svg></svg>",
		"filename": "test_gas.svg",
		"raw_ocr_text": "SHELL OIL #48102\nPUMP 04 REGULAR\nTOTAL PAID $45.00\nDATE: 2026-09-29",
		"merchant": "Shell Oil Station",
		"amount": 45.00,
		"category": "transport"
	}`
	reqScan := httptest.NewRequest(http.MethodPost, "/api/receipts/scan", strings.NewReader(scanJSON))
	reqScan.Header.Set("Content-Type", "application/json")
	reqScan.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recScan := httptest.NewRecorder()
	application.ServeHTTP(recScan, reqScan)
	if recScan.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from POST /api/receipts/scan, got %d: %s", recScan.Code, recScan.Body.String())
	}
	var scanRes struct {
		Success bool        `json:"success"`
		Receipt app.Receipt `json:"receipt"`
	}
	if err := json.Unmarshal(recScan.Body.Bytes(), &scanRes); err != nil {
		t.Fatalf("failed to decode scan response: %v", err)
	}
	if scanRes.Receipt.ID == 0 || scanRes.Receipt.TotalAmount != 45.00 {
		t.Fatalf("unexpected scanned receipt: %+v", scanRes.Receipt)
	}

	// 7. Test HTTP API POST /api/receipts/{id}/convert (Convert receipt to transaction)
	convertJSON := `{
		"amount": 45.00,
		"category": "transport",
		"note": "Shell Oil Station - Fuel",
		"txn_type": "expense",
		"account_id": 0,
		"tags": "#fuel, #receipt",
		"date": "2026-09-29"
	}`
	reqConvert := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/receipts/%d/convert", scanRes.Receipt.ID), strings.NewReader(convertJSON))
	reqConvert.Header.Set("Content-Type", "application/json")
	reqConvert.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recConvert := httptest.NewRecorder()
	application.ServeHTTP(recConvert, reqConvert)
	if recConvert.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/receipts/{id}/convert, got %d: %s", recConvert.Code, recConvert.Body.String())
	}

	// Verify transaction was created and has receipt_url
	txs, err := store.GetTransactions(username)
	if err != nil || len(txs) == 0 {
		t.Fatalf("expected transaction created from receipt, got: %v (len: %d)", err, len(txs))
	}
	if txs[0].Amount != 45.00 || txs[0].ReceiptURL == "" {
		t.Fatalf("expected transaction amount 45.00 and non-empty ReceiptURL, got: %+v", txs[0])
	}

	// 8. Test DELETE /api/receipts/{id}
	reqDel := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/receipts/%d", receipt.ID), nil)
	reqDel.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	recDel := httptest.NewRecorder()
	application.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from DELETE /api/receipts/%d, got %d", receipt.ID, recDel.Code)
	}
}


