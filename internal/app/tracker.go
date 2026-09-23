package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// CalculateTrends aggregates income and expense totals for the specified timeframe or date range.
func CalculateTrends(txs []Transaction, timeframe, startDateStr, endDateStr string) []MonthlySummary {
	timeframe = strings.ToLower(strings.TrimSpace(timeframe))
	if timeframe == "" && startDateStr == "" && endDateStr == "" {
		timeframe = "6m"
	}

	now := time.Now()

	// Helper for monthly aggregation given a list of year-month keys
	type monthKey struct {
		year  int
		month time.Month
	}

	aggregateMonths := func(keys []monthKey, filterStart, filterEnd time.Time) []MonthlySummary {
		sums := make(map[monthKey]*MonthlySummary, len(keys))
		for _, k := range keys {
			label := fmt.Sprintf("%s %02d", k.month.String()[:3], k.year%100)
			sums[k] = &MonthlySummary{Month: label}
		}

		for _, t := range txs {
			if !filterStart.IsZero() && t.Date.Before(filterStart) {
				continue
			}
			if !filterEnd.IsZero() && t.Date.After(filterEnd) {
				continue
			}
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

	// 1. Check custom range
	if timeframe == "custom" || (startDateStr != "" && endDateStr != "") {
		parseDate := func(s string, isEnd bool) (time.Time, bool) {
			s = strings.TrimSpace(s)
			if t, err := time.Parse("2006-01-02", s); err == nil {
				if isEnd {
					return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.Local), true
				}
				return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), true
			}
			if t, err := time.Parse("2006-01", s); err == nil {
				if isEnd {
					lastDay := time.Date(t.Year(), t.Month()+1, 0, 23, 59, 59, 999999999, time.Local)
					return lastDay, true
				}
				return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local), true
			}
			return time.Time{}, false
		}

		start, okStart := parseDate(startDateStr, false)
		end, okEnd := parseDate(endDateStr, true)

		if okStart && okEnd {
			if start.After(end) {
				start, end = end, start
			}

			// If range is <= 31 days, aggregate daily
			if end.Sub(start) <= 32*24*time.Hour {
				type dayKey struct {
					year  int
					month time.Month
					day   int
				}
				var dayKeys []dayKey
				cur := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
				endLimit := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.Local)
				for !cur.After(endLimit) {
					dayKeys = append(dayKeys, dayKey{year: cur.Year(), month: cur.Month(), day: cur.Day()})
					cur = cur.AddDate(0, 0, 1)
				}

				sums := make(map[dayKey]*MonthlySummary, len(dayKeys))
				for _, k := range dayKeys {
					label := fmt.Sprintf("%s %02d", k.month.String()[:3], k.day)
					sums[k] = &MonthlySummary{Month: label}
				}

				for _, t := range txs {
					if t.Date.Before(start) || t.Date.After(end) {
						continue
					}
					k := dayKey{year: t.Date.Year(), month: t.Date.Month(), day: t.Date.Day()}
					if s, ok := sums[k]; ok {
						if t.Type == "income" {
							s.Income += t.Amount
						} else if t.Type == "expense" {
							s.Expense += t.Amount
						}
					}
				}

				result := make([]MonthlySummary, len(dayKeys))
				for i, k := range dayKeys {
					result[i] = *sums[k]
				}
				return result
			}

			// Range > 31 days: aggregate monthly
			var monthKeys []monthKey
			cur := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.Local)
			endMonth := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.Local)
			for !cur.After(endMonth) {
				monthKeys = append(monthKeys, monthKey{year: cur.Year(), month: cur.Month()})
				cur = cur.AddDate(0, 1, 0)
			}
			return aggregateMonths(monthKeys, start, end)
		}
		// If custom dates invalid, fallback to 6m
		timeframe = "6m"
	}

	// 2. YTD
	if timeframe == "ytd" {
		curMonth := int(now.Month())
		keys := make([]monthKey, curMonth)
		for m := 1; m <= curMonth; m++ {
			keys[m-1] = monthKey{year: now.Year(), month: time.Month(m)}
		}
		return aggregateMonths(keys, time.Time{}, time.Time{})
	}

	// 3. All Time
	if timeframe == "all" {
		if len(txs) == 0 {
			timeframe = "6m"
		} else {
			earliest := now
			for _, t := range txs {
				if t.Date.Before(earliest) {
					earliest = t.Date
				}
			}
			startYear, startMonth := earliest.Year(), earliest.Month()
			totalMonths := (now.Year()-startYear)*12 + int(now.Month()) - int(startMonth) + 1
			if totalMonths < 1 {
				totalMonths = 1
			}
			if totalMonths > 60 {
				totalMonths = 60
			}
			keys := make([]monthKey, totalMonths)
			startCur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(totalMonths - 1), 0)
			for i := 0; i < totalMonths; i++ {
				d := startCur.AddDate(0, i, 0)
				keys[i] = monthKey{year: d.Year(), month: d.Month()}
			}
			return aggregateMonths(keys, time.Time{}, time.Time{})
		}
	}

	// 4. Presets: "3m", "6m", "12m", or numeric
	monthsBack := 6
	if strings.HasSuffix(timeframe, "m") {
		if n, err := strconv.Atoi(strings.TrimSuffix(timeframe, "m")); err == nil && n > 0 {
			monthsBack = n
		}
	} else if n, err := strconv.Atoi(timeframe); err == nil && n > 0 {
		monthsBack = n
	}
	if monthsBack > 60 {
		monthsBack = 60
	}

	keys := make([]monthKey, monthsBack)
	startCur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(monthsBack - 1), 0)
	for i := 0; i < monthsBack; i++ {
		d := startCur.AddDate(0, i, 0)
		keys[i] = monthKey{year: d.Year(), month: d.Month()}
	}
	return aggregateMonths(keys, time.Time{}, time.Time{})
}

// GetTrendsByTimeFrame returns aggregated income & expense totals for a specific timeframe.
func (bt *BudgetTracker) GetTrendsByTimeFrame(timeframe, startDateStr, endDateStr string) []MonthlySummary {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	return CalculateTrends(bt.Transactions, timeframe, startDateStr, endDateStr)
}

// GetMonthlyTrends returns aggregated income & expense totals for the past N months.
func (bt *BudgetTracker) GetMonthlyTrends(monthsBack int) []MonthlySummary {
	if monthsBack <= 0 {
		monthsBack = 6
	}
	return bt.GetTrendsByTimeFrame(fmt.Sprintf("%dm", monthsBack), "", "")
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
