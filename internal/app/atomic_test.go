package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func financialSnapshot(t *testing.T, store *DBStore) string {
	t.Helper()
	snapshot := make(map[string][][]any)
	for _, table := range []string{"goals", "goal_contributions", "transactions", "debts", "subscriptions", "splits", "split_participants"} {
		rows, err := store.db.Query("SELECT * FROM " + table + " ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			snapshot[table] = append(snapshot[table], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFinancialWritesRollback(t *testing.T) {
	for _, tc := range []struct{ name, table, event string }{
		{"deposit", "transactions", "INSERT"}, {"contribution", "goal_contributions", "INSERT"},
		{"withdraw", "transactions", "INSERT"}, {"debt", "transactions", "INSERT"},
		{"subscription", "subscriptions", "UPDATE"}, {"split", "split_participants", "INSERT"},
		{"split_debt", "debts", "INSERT"}, {"settlement", "transactions", "INSERT"},
		{"offset", "debts", "UPDATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testStore(t)
			goal, err := db.CreateGoal("alice", "Savings", 1000, nil, "", "", "savings")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.DepositToGoal(goal.ID, "alice", 100, "seed", false); err != nil {
				t.Fatal(err)
			}
			debt, err := db.CreateDebt("alice", "Bob", "owing_me", 100, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.CreateDebt("alice", "Bob", "i_owe", 50, nil, ""); err != nil {
				t.Fatal(err)
			}
			sub, err := db.AddSubscription("alice", "Internet", 10, "bills", "monthly", time.Now().AddDate(0, 1, 0))
			if err != nil {
				t.Fatal(err)
			}
			before := financialSnapshot(t, db)
			when := ""
			if tc.name == "offset" {
				when = " WHEN OLD.type = 'i_owe'"
			}
			trigger := fmt.Sprintf("CREATE TRIGGER fail_write BEFORE %s ON %s%s BEGIN SELECT RAISE(ABORT, 'injected failure'); END", tc.event, tc.table, when)
			if _, err = db.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "deposit", "contribution":
				_, err = db.DepositToGoal(goal.ID, "alice", 20, "test", true)
			case "withdraw":
				_, err = db.WithdrawFromGoal(goal.ID, "alice", 20, "test", true)
			case "debt":
				_, err = db.RecordDebtPayment(debt.ID, "alice", 20, time.Now(), true)
			case "subscription":
				_, err = db.PaySubscription(sub.ID, "alice")
			case "split", "split_debt":
				_, err = db.CreateSplitExpense("alice", "Lunch", "food", "", 100, time.Now(), "You", true, "equal", []SplitParticipantInput{{Name: "You", IsUser: true, ShareAmount: 50}, {Name: "Bob", ShareAmount: 50}}, true, 0)
			case "settlement":
				_, err = db.SettleContactDebts("alice", "Bob", "full", 0, true)
			case "offset":
				_, err = db.SettleContactDebts("alice", "Bob", "offset_only", 0, false)
			}
			if err == nil {
				t.Fatal("expected injected failure to reach caller")
			}
			if after := financialSnapshot(t, db); after != before {
				t.Fatalf("partial financial update persisted\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}
