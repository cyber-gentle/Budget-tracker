package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Transaction holds each transaction's details.
type Transaction struct {
	ID          int       `json:"id"`
	Amount      float64   `json:"amount"`
	Category    string    `json:"category"`
	Note        string    `json:"note"`
	Date        time.Time `json:"date"`
	Type        string    `json:"type"` // "income" or "expense"
	Tags        string    `json:"tags,omitempty"`
	TagList     []string  `json:"tag_list,omitempty"`
	AccountID   int       `json:"account_id,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	AccountIcon string    `json:"account_icon,omitempty"`
	ReceiptURL  string    `json:"receipt_url,omitempty"`
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

// SortTransactionsNewestFirst returns transactions sorted by date descending.
func SortTransactionsNewestFirst(txs []Transaction) {
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].Date.After(txs[j].Date)
	})
}
