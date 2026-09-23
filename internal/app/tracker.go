package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Transaction holds each transaction's details.
type Transaction struct {
	ID       int       `json:"id"`
	Amount   float64   `json:"amount"`
	Category string    `json:"category"`
	Note     string    `json:"note"`
	Date     time.Time `json:"date"`
	Type     string    `json:"type"` // "income" or "expense"
}

// FinancialRecord defines common behavior for financial entities.
type FinancialRecord interface {
	GetAmount() float64
	GetType() string
}

// GetAmount implements FinancialRecord.
func (t Transaction) GetAmount() float64 { return t.Amount }

// GetType implements FinancialRecord.
func (t Transaction) GetType() string { return t.Type }

// MonthlySummary holds aggregated data for trends.
type MonthlySummary struct {
	Month   string  `json:"month"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
}

// BudgetTracker manages transactions and budgets for a user with concurrent access.
type BudgetTracker struct {
	mu           sync.RWMutex
	Transactions []Transaction      `json:"transactions"`
	Budgets      map[string]float64 `json:"budgets,omitempty"`  // category -> monthly limit
	Currency     string             `json:"currency,omitempty"` // default "₦"
	NextID       int                `json:"next_id"`
	filePath     string
}

// NewBudgetTracker creates a tracker loaded from a JSON file or fresh.
func NewBudgetTracker(filePath string) *BudgetTracker {
	bt := &BudgetTracker{
		filePath: filePath,
		Budgets:  make(map[string]float64),
		Currency: "₦",
		NextID:   1,
	}
	bt.load()
	if bt.Budgets == nil {
		bt.Budgets = make(map[string]float64)
	}
	if bt.Currency == "" {
		bt.Currency = "₦"
	}
	return bt
}

func (bt *BudgetTracker) load() {
	data, err := os.ReadFile(bt.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return // fresh start
		}
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", bt.filePath, err)
		return
	}
	if err := json.Unmarshal(data, bt); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", bt.filePath, err)
	}
}

func (bt *BudgetTracker) save() error {
	if err := os.MkdirAll(filepath.Dir(bt.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(bt.filePath, data, 0644)
}

// AddTransaction adds a new transaction with an optional custom date.
func (bt *BudgetTracker) AddTransaction(amount float64, category, note, txnType string, optDate ...time.Time) Transaction {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	txDate := time.Now()
	if len(optDate) > 0 && !optDate[0].IsZero() {
		txDate = optDate[0]
	}

	t := Transaction{
		ID:       bt.NextID,
		Amount:   amount,
		Category: category,
		Note:     note,
		Type:     txnType,
		Date:     txDate,
	}
	bt.Transactions = append(bt.Transactions, t)
	bt.NextID++
	bt.save()
	return t
}

// UpdateTransaction updates fields of an existing transaction by ID.
func (bt *BudgetTracker) UpdateTransaction(id int, amount float64, category, note, txnType string, optDate ...time.Time) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	for i, t := range bt.Transactions {
		if t.ID == id {
			bt.Transactions[i].Amount = amount
			bt.Transactions[i].Category = category
			bt.Transactions[i].Note = note
			bt.Transactions[i].Type = txnType
			if len(optDate) > 0 && !optDate[0].IsZero() {
				bt.Transactions[i].Date = optDate[0]
			}
			bt.save()
			return true
		}
	}
	return false
}

// DeleteTransaction removes a transaction by ID. Returns true if found.
func (bt *BudgetTracker) DeleteTransaction(id int) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	idx := -1
	for i, t := range bt.Transactions {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false
	}
	bt.Transactions = append(bt.Transactions[:idx], bt.Transactions[idx+1:]...)
	bt.save()
	return true
}

// GetTransactions returns a copy of all transactions.
func (bt *BudgetTracker) GetTransactions() []Transaction {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	out := make([]Transaction, len(bt.Transactions))
	copy(out, bt.Transactions)
	return out
}

// CalculateTotal returns total for a given type ("income" or "expense").
func (bt *BudgetTracker) CalculateTotal(txnType string) float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	var total float64
	for _, t := range bt.Transactions {
		if t.Type == txnType {
			total += t.Amount
		}
	}
	return total
}

// Balance returns income minus expenses.
func (bt *BudgetTracker) Balance() float64 {
	return bt.CalculateTotal("income") - bt.CalculateTotal("expense")
}

// SetBudget sets or updates the monthly budget limit for a category.
func (bt *BudgetTracker) SetBudget(category string, limit float64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	if bt.Budgets == nil {
		bt.Budgets = make(map[string]float64)
	}
	if limit <= 0 {
		delete(bt.Budgets, category)
	} else {
		bt.Budgets[category] = limit
	}
	bt.save()
}

// GetBudgets returns a copy of current category budgets.
func (bt *BudgetTracker) GetBudgets() map[string]float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	b := make(map[string]float64)
	for k, v := range bt.Budgets {
		b[k] = v
	}
	return b
}

// SetCurrency sets user's preferred currency symbol.
func (bt *BudgetTracker) SetCurrency(curr string) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	if curr != "" {
		bt.Currency = curr
		bt.save()
	}
}

// GetCurrency returns user's currency symbol.
func (bt *BudgetTracker) GetCurrency() string {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	if bt.Currency == "" {
		return "₦"
	}
	return bt.Currency
}

// GetCurrentMonthSpending calculates current month expenses grouped by category.
func (bt *BudgetTracker) GetCurrentMonthSpending() map[string]float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	now := time.Now()
	res := make(map[string]float64)
	for _, t := range bt.Transactions {
		if t.Type == "expense" && t.Date.Year() == now.Year() && t.Date.Month() == now.Month() {
			res[t.Category] += t.Amount
		}
	}
	return res
}

// GetMonthlyTrends returns aggregated income & expense totals for the past N months.
func (bt *BudgetTracker) GetMonthlyTrends(monthsBack int) []MonthlySummary {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	if monthsBack <= 0 {
		monthsBack = 6
	}

	now := time.Now()
	// Build ordered list of recent months
	type monthKey struct {
		year  int
		month time.Month
	}
	keys := make([]monthKey, monthsBack)
	for i := 0; i < monthsBack; i++ {
		// e.g. i=0 is current month, i=1 is 1 month ago
		d := time.Date(now.Year(), now.Month()-time.Month(monthsBack-1-i), 1, 0, 0, 0, 0, time.UTC)
		keys[i] = monthKey{year: d.Year(), month: d.Month()}
	}

	sums := make(map[monthKey]*MonthlySummary)
	for _, k := range keys {
		label := fmt.Sprintf("%s %02d", k.month.String()[:3], k.year%100)
		sums[k] = &MonthlySummary{Month: label}
	}

	for _, t := range bt.Transactions {
		k := monthKey{year: t.Date.Year(), month: t.Date.Month()}
		if s, ok := sums[k]; ok {
			if t.Type == "income" {
				s.Income += t.Amount
			} else if t.Type == "expense" {
				s.Expense += t.Amount
			}
		}
	}

	result := make([]MonthlySummary, len(keys))
	for i, k := range keys {
		result[i] = *sums[k]
	}
	return result
}

// CategoryBreakdown returns all-time or monthly expense totals per category.
func (bt *BudgetTracker) CategoryBreakdown(allTime bool) map[string]float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()

	now := time.Now()
	res := make(map[string]float64)
	for _, t := range bt.Transactions {
		if t.Type != "expense" {
			continue
		}
		if allTime || (t.Date.Year() == now.Year() && t.Date.Month() == now.Month()) {
			res[t.Category] += t.Amount
		}
	}
	return res
}

// SortTransactionsNewestFirst returns transactions sorted by date descending.
func SortTransactionsNewestFirst(txs []Transaction) {
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].Date.After(txs[j].Date)
	})
}
