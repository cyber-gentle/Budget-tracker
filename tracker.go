package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// BudgetTracker manages transactions for a user with concurrent access.
type BudgetTracker struct {
	mu           sync.RWMutex
	Transactions []Transaction `json:"transactions"`
	NextID       int           `json:"next_id"`
	filePath     string
}

// NewBudgetTracker creates a tracker loaded from a JSON file or fresh.
func NewBudgetTracker(filePath string) *BudgetTracker {
	bt := &BudgetTracker{
		filePath: filePath,
		NextID:   1,
	}
	bt.load()
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

// AddTransaction adds a new transaction.
func (bt *BudgetTracker) AddTransaction(amount float64, category, note, txnType string) Transaction {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	t := Transaction{
		ID:       bt.NextID,
		Amount:   amount,
		Category: category,
		Note:     note,
		Type:     txnType,
		Date:     time.Now(),
	}
	bt.Transactions = append(bt.Transactions, t)
	bt.NextID++
	bt.save()
	return t
}

// UpdateTransaction updates fields of an existing transaction by ID.
func (bt *BudgetTracker) UpdateTransaction(id int, amount float64, category, note, txnType string) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	for i, t := range bt.Transactions {
		if t.ID == id {
			bt.Transactions[i].Amount = amount
			bt.Transactions[i].Category = category
			bt.Transactions[i].Note = note
			bt.Transactions[i].Type = txnType
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
