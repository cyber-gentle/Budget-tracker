package main

// Import the required packages
import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Transactional struct to hold each transactions's details
type Transaction struct {
	ID       int
	Amount   float64
	Category string
	Date     time.Time
	Type     string
}

// BudgetTracker struct to manage transactions
type BudgetTracker struct {
	transactions []Transaction
	NextID       int
}

// Interface for common behavior
type FinancialRecord interface {
	GetAmount() float64
	GetType() string
}

// Implement interface methods for Transaction struct
func (t Transaction) GetAmount() float64 {
	return t.Amount
}

func (t Transaction) GetType() string {
	return t.Type
}

// Add a new transaction
func (bt *BudgetTracker) AddTransaction(amount float64, category, txnType string) {
	newTransaction := Transaction{
		ID:       bt.NextID,
		Amount:   amount,
		Category: category,
		Type:     txnType,
		Date:     time.Now(),
	}
	bt.transactions = append(bt.transactions, newTransaction)
	bt.NextID++
}

// Creating DisplayTransactions method
func (bt BudgetTracker) DisplayTransactions() {
	fmt.Println("ID\tAmount\tCategory\tDate\tType")
	for _, transaction := range bt.transactions {
		fmt.Printf("%d\t%.2f\t%s\t%s\t%s\n", transaction.ID,
			transaction.Amount, transaction.Category,
			transaction.Date.Format("02-01-2026"), transaction.Type)
	}

}

// Get total Income or Expenses
func (bt BudgetTracker) CalculateTotal(txnType string) float64 {
	var total float64
	for _, transaction := range bt.transactions {
		if transaction.Type == txnType {
			total += transaction.Amount
		}
	}
	return total
}

// Save the transactions to a csv file

func (bt BudgetTracker) SaveToCSV(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file) // creating a new csv file
	defer writer.Flush()          // flush make sure that the data is
	//  written before the file is closed

	// Write the CSV header
	writer.Write([]string{"ID", "Amount", "Category", "Date", "Type"})

	for _, transaction := range bt.transactions {
		record := []string{
			strconv.Itoa(transaction.ID),
			fmt.Sprintf("%.2f", transaction.Amount),
			transaction.Category,
			transaction.Date.Format("2026-3-10"),
			transaction.Type,
		}
		writer.Write(record)
	}

	return nil
}
