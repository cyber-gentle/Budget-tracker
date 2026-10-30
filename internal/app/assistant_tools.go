package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─── Assistant tools ────────────────────────────────────────────────────────
//
// Read tools return figures the app calculates, so the model explains numbers
// instead of inventing them. propose_* tools only build a draft for the user to
// confirm; nothing here writes data. Execution happens in executeAssistantAction,
// reached solely through HandleAssistantConfirm.

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

var (
	pDate     = prop("string", "Date as YYYY-MM-DD")
	pCategory = prop("string", "Category slug, e.g. food")
)

var assistantTools = []toolDef{
	{Name: "get_overview", Description: "Current month income, expenses and net so far, all-time balance, account balances, how far through the month we are, and how far back the user's records go.", Params: obj(map[string]any{})},
	{Name: "get_spending", Description: "Totals for a date range: income, expenses, net, spending by category and the largest expenses. Defaults to the current month.",
		Params: obj(map[string]any{"start_date": pDate, "end_date": pDate, "category": pCategory, "type": prop("string", "income or expense")})},
	{Name: "compare_periods", Description: "Compare spending and income between two date ranges, with per-category differences. Use for 'am I spending more' questions.",
		Params: obj(map[string]any{"a_start": pDate, "a_end": pDate, "b_start": pDate, "b_end": pDate}, "a_start", "a_end", "b_start", "b_end")},
	{Name: "get_budgets", Description: "Monthly budgets: limit, spent this month, remaining and status per category.", Params: obj(map[string]any{})},
	{Name: "get_goals", Description: "Savings goals with saved, target, remaining, target date and the monthly amount needed to hit the target date.", Params: obj(map[string]any{})},
	{Name: "get_debts", Description: "Open debts (owed by and to the user) with remaining amounts and due dates.", Params: obj(map[string]any{})},
	{Name: "get_subscriptions", Description: "Recurring bills and subscriptions with amounts, cycles, next due dates and monthly-equivalent total.", Params: obj(map[string]any{})},
	{Name: "find_transactions", Description: "Search the user's transactions to identify specific ones (returns ids). Use before proposing an edit or deletion.",
		Params: obj(map[string]any{"text": prop("string", "Text to match in the note or category"), "start_date": pDate, "end_date": pDate, "category": pCategory, "type": prop("string", "income or expense"), "limit": prop("integer", "Max results, up to 20")})},
	{Name: "simulate_expense", Description: "What-if: the effect of a planned expense on balance, the category budget, and the 30-day cash-flow forecast. Use for 'can I afford' questions.",
		Params: obj(map[string]any{"amount": prop("number", "Planned expense amount"), "date": pDate, "category": pCategory, "title": prop("string", "Short label")}, "amount")},

	{Name: "propose_add_transaction", Description: "Draft a new transaction for the user to confirm. Does not save.",
		Params: obj(map[string]any{"amount": prop("number", "Amount, positive"), "type": prop("string", "income or expense"), "category": pCategory, "note": prop("string", "Short description"), "date": pDate, "account_id": prop("integer", "Account id, or 0 for default")}, "amount", "type")},
	{Name: "propose_update_transaction", Description: "Draft an edit to an existing transaction (by id from find_transactions) for the user to confirm. Omit fields that don't change. Does not save.",
		Params: obj(map[string]any{"transaction_id": prop("integer", "Transaction id"), "amount": prop("number", "New amount"), "type": prop("string", "income or expense"), "category": pCategory, "note": prop("string", "New note"), "date": pDate}, "transaction_id")},
	{Name: "propose_delete_transaction", Description: "Draft deleting a transaction (by id from find_transactions) for the user to confirm. Does not delete.",
		Params: obj(map[string]any{"transaction_id": prop("integer", "Transaction id")}, "transaction_id")},
	{Name: "propose_set_budget", Description: "Draft setting a category's monthly budget limit for the user to confirm. Does not save.",
		Params: obj(map[string]any{"category": pCategory, "monthly_limit": prop("number", "New monthly limit, positive")}, "category", "monthly_limit")},
	{Name: "propose_goal_contribution", Description: "Draft a deposit to or withdrawal from a savings goal for the user to confirm. Does not save.",
		Params: obj(map[string]any{"goal_id": prop("integer", "Goal id from get_goals"), "amount": prop("number", "Amount, positive"), "direction": prop("string", "deposit or withdraw")}, "goal_id", "amount", "direction")},
	{Name: "propose_debt_payment", Description: "Draft recording a payment against a debt for the user to confirm. Does not save.",
		Params: obj(map[string]any{"debt_id": prop("integer", "Debt id from get_debts"), "amount": prop("number", "Payment amount, positive")}, "debt_id", "amount")},
}

// assistantSession executes tool calls for one chat request.
type assistantSession struct {
	app      *App
	username string
	curr     string
	now      time.Time
	draft    *AssistantAction
}

func (s *assistantSession) money(v float64) string { return formatMoney(v, s.curr) }

func argStr(args map[string]any, k string) string {
	if v, ok := args[k]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func argNum(args map[string]any, k string) float64 {
	switch v := args[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

func argInt(args map[string]any, k string) int { return int(math.Round(argNum(args, k))) }

func dayOrDefault(s string, def time.Time) time.Time {
	if d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local); err == nil {
		return d
	}
	return def
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.Local)
}

func (s *assistantSession) exec(c toolCall) any {
	if c.Args == nil {
		c.Args = map[string]any{}
	}
	switch c.Name {
	case "get_overview":
		return s.overview()
	case "get_spending":
		start := dayOrDefault(argStr(c.Args, "start_date"), startOfMonth(s.now))
		end := dayOrDefault(argStr(c.Args, "end_date"), s.now)
		return s.spending(start, end, argStr(c.Args, "category"), argStr(c.Args, "type"))
	case "compare_periods":
		return s.compare(c.Args)
	case "get_budgets":
		return s.budgets()
	case "get_goals":
		return s.goals()
	case "get_debts":
		return s.debts()
	case "get_subscriptions":
		return s.subscriptions()
	case "find_transactions":
		return s.findTransactions(c.Args)
	case "simulate_expense":
		return s.simulate(c.Args)
	}
	if strings.HasPrefix(c.Name, "propose_") {
		return s.propose(c)
	}
	return map[string]any{"error": "unknown tool " + c.Name}
}

// ─── Read tools ─────────────────────────────────────────────────────────────

type periodAgg struct {
	income, expense float64
	count           int
	byCat           map[string]float64
	largest         []Transaction
}

func (s *assistantSession) aggregate(start, end time.Time, category, typ string) periodAgg {
	txs, _ := s.app.DB.GetTransactions(s.username)
	agg := periodAgg{byCat: map[string]float64{}}
	endT := endOfDay(end)
	for _, t := range txs {
		if t.Date.Before(start) || t.Date.After(endT) {
			continue
		}
		if category != "" && !strings.EqualFold(t.Category, category) {
			continue
		}
		if typ != "" && !strings.EqualFold(t.Type, typ) {
			continue
		}
		agg.count++
		if t.Type == "income" {
			agg.income += t.Amount
		} else {
			agg.expense += t.Amount
			agg.byCat[t.Category] += t.Amount
			agg.largest = append(agg.largest, t)
		}
	}
	sort.Slice(agg.largest, func(i, j int) bool { return agg.largest[i].Amount > agg.largest[j].Amount })
	if len(agg.largest) > 5 {
		agg.largest = agg.largest[:5]
	}
	return agg
}

func (s *assistantSession) txView(t Transaction) map[string]any {
	return map[string]any{"id": t.ID, "date": t.Date.Format("2006-01-02"), "type": t.Type, "amount": s.money(t.Amount),
		"category": t.Category, "note": t.Note, "account": t.AccountName}
}

func (s *assistantSession) spending(start, end time.Time, category, typ string) map[string]any {
	agg := s.aggregate(start, end, category, typ)
	cats := make([]string, 0, len(agg.byCat))
	for c := range agg.byCat {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return agg.byCat[cats[i]] > agg.byCat[cats[j]] })
	byCat := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		share := 0.0
		if agg.expense > 0 {
			share = math.Round(agg.byCat[c]/agg.expense*1000) / 10
		}
		byCat = append(byCat, map[string]any{"category": c, "spent": s.money(agg.byCat[c]), "share_pct": share})
	}
	largest := make([]map[string]any, 0, len(agg.largest))
	for _, t := range agg.largest {
		largest = append(largest, s.txView(t))
	}
	return map[string]any{
		"start_date": start.Format("2006-01-02"), "end_date": end.Format("2006-01-02"),
		"period_in_progress": !endOfDay(end).Before(s.now),
		"transaction_count":  agg.count,
		"income":             s.money(agg.income), "expenses": s.money(agg.expense), "net": s.money(agg.income - agg.expense),
		"spending_by_category": byCat, "largest_expenses": largest,
	}
}

func (s *assistantSession) overview() map[string]any {
	income, expense, balance, _ := s.app.DB.CalculateTotals(s.username)
	month := s.aggregate(startOfMonth(s.now), s.now, "", "")
	daysInMonth := startOfMonth(s.now).AddDate(0, 1, -1).Day()

	txs, _ := s.app.DB.GetTransactions(s.username)
	earliest := ""
	if len(txs) > 0 {
		earliest = txs[len(txs)-1].Date.Format("2006-01-02")
	}
	accounts, _ := s.app.DB.GetAccounts(s.username)
	accts := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		accts = append(accts, map[string]any{"id": a.ID, "name": a.Name, "type": a.Type, "balance": s.money(a.CurrentBalance), "default": a.IsDefault})
	}
	return map[string]any{
		"today": s.now.Format("2006-01-02"), "day_of_month": s.now.Day(), "days_in_month": daysInMonth,
		"this_month": map[string]any{"income": s.money(month.income), "expenses": s.money(month.expense), "net": s.money(month.income - month.expense), "transaction_count": month.count},
		"all_time":   map[string]any{"income": s.money(income), "expenses": s.money(expense), "balance": s.money(balance)},
		"accounts":   accts, "total_transactions": len(txs), "records_start": earliest,
	}
}

func (s *assistantSession) compare(args map[string]any) any {
	aS, aE := dayOrDefault(argStr(args, "a_start"), time.Time{}), dayOrDefault(argStr(args, "a_end"), time.Time{})
	bS, bE := dayOrDefault(argStr(args, "b_start"), time.Time{}), dayOrDefault(argStr(args, "b_end"), time.Time{})
	if aS.IsZero() || aE.IsZero() || bS.IsZero() || bE.IsZero() {
		return map[string]any{"error": "all four dates are required as YYYY-MM-DD"}
	}
	a, b := s.aggregate(aS, aE, "", ""), s.aggregate(bS, bE, "", "")
	pct := func(from, to float64) any {
		if from == 0 {
			return nil
		}
		return math.Round((to-from)/from*1000) / 10
	}
	cats := map[string]bool{}
	for c := range a.byCat {
		cats[c] = true
	}
	for c := range b.byCat {
		cats[c] = true
	}
	type diff struct {
		cat   string
		delta float64
	}
	var diffs []diff
	for c := range cats {
		diffs = append(diffs, diff{c, b.byCat[c] - a.byCat[c]})
	}
	sort.Slice(diffs, func(i, j int) bool { return math.Abs(diffs[i].delta) > math.Abs(diffs[j].delta) })
	byCat := make([]map[string]any, 0, len(diffs))
	for _, d := range diffs {
		byCat = append(byCat, map[string]any{"category": d.cat, "period_a": s.money(a.byCat[d.cat]), "period_b": s.money(b.byCat[d.cat]),
			"change": s.money(d.delta), "change_pct": pct(a.byCat[d.cat], b.byCat[d.cat])})
	}
	return map[string]any{
		"period_a":           map[string]any{"start": aS.Format("2006-01-02"), "end": aE.Format("2006-01-02"), "income": s.money(a.income), "expenses": s.money(a.expense), "in_progress": !endOfDay(aE).Before(s.now)},
		"period_b":           map[string]any{"start": bS.Format("2006-01-02"), "end": bE.Format("2006-01-02"), "income": s.money(b.income), "expenses": s.money(b.expense), "in_progress": !endOfDay(bE).Before(s.now)},
		"expense_change":     s.money(b.expense - a.expense),
		"expense_change_pct": pct(a.expense, b.expense),
		"category_changes":   byCat,
	}
}

func (s *assistantSession) budgets() any {
	limits, _ := s.app.DB.GetBudgets(s.username)
	spent, _ := s.app.DB.GetCurrentMonthSpending(s.username)
	keys := make([]string, 0, len(limits))
	for k := range limits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		used := 0.0
		if limits[k] > 0 {
			used = math.Round(spent[k]/limits[k]*1000) / 10
		}
		status := "on_track"
		if spent[k] > limits[k] {
			status = "over"
		} else if used >= 80 {
			status = "near_limit"
		}
		out = append(out, map[string]any{"category": k, "limit": s.money(limits[k]), "spent_this_month": s.money(spent[k]),
			"remaining": s.money(limits[k] - spent[k]), "used_pct": used, "status": status})
	}
	daysInMonth := startOfMonth(s.now).AddDate(0, 1, -1).Day()
	return map[string]any{"budgets": out, "month_elapsed_pct": math.Round(float64(s.now.Day())/float64(daysInMonth)*1000) / 10}
}

func (s *assistantSession) goals() any {
	goals, summary, _ := s.app.DB.GetGoals(s.username)
	out := make([]map[string]any, 0, len(goals))
	for _, g := range goals {
		item := map[string]any{"id": g.ID, "name": g.Name, "saved": s.money(g.SavedAmount), "target": s.money(g.TargetAmount),
			"remaining": s.money(g.Remaining), "progress_pct": g.Percentage, "status": g.Status}
		if g.TargetDate != nil {
			item["target_date"] = g.TargetDate.Format("2006-01-02")
			months := g.TargetDate.Sub(s.now).Hours() / 24 / 30.44
			if months > 0 && g.Remaining > 0 {
				item["monthly_needed"] = s.money(g.Remaining / math.Max(months, 1))
				item["months_left"] = math.Round(months*10) / 10
			}
		}
		out = append(out, item)
	}
	return map[string]any{"goals": out, "total_saved": s.money(summary.TotalSaved), "total_target": s.money(summary.TotalTarget)}
}

func (s *assistantSession) debts() any {
	debts, summary, _ := s.app.DB.GetDebts(s.username)
	out := make([]map[string]any, 0, len(debts))
	for _, d := range debts {
		if d.Status == "settled" {
			continue
		}
		item := map[string]any{"id": d.ID, "person": d.PersonName, "direction": d.Type, "original": s.money(d.Amount),
			"paid": s.money(d.AmountPaid), "remaining": s.money(d.Remaining), "status": d.Status, "note": d.Note}
		if d.DueDate != nil {
			item["due_date"] = d.DueDate.Format("2006-01-02")
		}
		out = append(out, item)
	}
	res := map[string]any{"open_debts": out, "direction_legend": "i_owe = the user owes; owing_me = owed to the user"}
	if summary != nil {
		res["total_user_owes"] = s.money(summary.TotalUserOwes)
		res["total_owed_to_user"] = s.money(summary.TotalOwedToUser)
	}
	return res
}

func (s *assistantSession) subscriptions() any {
	subs, _ := s.app.DB.GetSubscriptions(s.username)
	out := make([]map[string]any, 0, len(subs))
	monthly := 0.0
	for _, sub := range subs {
		perMonth := sub.Amount
		switch sub.BillingCycle {
		case "weekly":
			perMonth = sub.Amount * 52 / 12
		case "yearly":
			perMonth = sub.Amount / 12
		}
		if sub.Status == "active" {
			monthly += perMonth
		}
		out = append(out, map[string]any{"id": sub.ID, "name": sub.Name, "amount": s.money(sub.Amount), "cycle": sub.BillingCycle,
			"monthly_equivalent": s.money(perMonth), "next_due": sub.NextDueDate.Format("2006-01-02"), "status": sub.Status})
	}
	return map[string]any{"subscriptions": out, "active_monthly_total": s.money(monthly)}
}

func (s *assistantSession) findTransactions(args map[string]any) any {
	txs, _ := s.app.DB.GetTransactions(s.username)
	text := strings.ToLower(argStr(args, "text"))
	start := dayOrDefault(argStr(args, "start_date"), time.Time{})
	end := dayOrDefault(argStr(args, "end_date"), time.Time{})
	category, typ := argStr(args, "category"), argStr(args, "type")
	limit := argInt(args, "limit")
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	out := []map[string]any{}
	for _, t := range txs {
		if !start.IsZero() && t.Date.Before(start) || !end.IsZero() && t.Date.After(endOfDay(end)) {
			continue
		}
		if category != "" && !strings.EqualFold(t.Category, category) || typ != "" && !strings.EqualFold(t.Type, typ) {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(t.Note+" "+t.Category), text) {
			continue
		}
		out = append(out, s.txView(t))
		if len(out) >= limit {
			break
		}
	}
	return map[string]any{"transactions": out, "count": len(out)}
}

func (s *assistantSession) simulate(args map[string]any) any {
	amount := argNum(args, "amount")
	if !validMoney(amount) || amount <= 0 {
		return map[string]any{"error": "amount must be a positive number"}
	}
	date := dayOrDefault(argStr(args, "date"), s.now)
	title := argStr(args, "title")
	if title == "" {
		title = "Planned expense"
	}
	_, _, balance, _ := s.app.DB.CalculateTotals(s.username)
	res := map[string]any{
		"planned_expense": s.money(amount), "date": date.Format("2006-01-02"),
		"balance_now": s.money(balance), "balance_after": s.money(balance - amount),
	}
	if cat := argStr(args, "category"); cat != "" {
		limits, _ := s.app.DB.GetBudgets(s.username)
		spent, _ := s.app.DB.GetCurrentMonthSpending(s.username)
		if limit, ok := limits[cat]; ok {
			res["budget"] = map[string]any{"category": cat, "limit": s.money(limit), "remaining_now": s.money(limit - spent[cat]),
				"remaining_after": s.money(limit - spent[cat] - amount), "would_exceed": spent[cat]+amount > limit}
		} else {
			res["budget"] = "no budget set for " + cat
		}
	}
	base, err1 := s.app.DB.GenerateCashFlowForecast(s.username, WhatIfSimulationParams{TimeframeDays: 30})
	with, err2 := s.app.DB.GenerateCashFlowForecast(s.username, WhatIfSimulationParams{TimeframeDays: 30,
		PlannedExpenseAmount: amount, PlannedExpenseDate: date.Format("2006-01-02"), PlannedExpenseTitle: title})
	if err1 == nil && err2 == nil {
		res["forecast_30_days"] = map[string]any{
			"lowest_balance_without": s.money(base.LowestProjectedBalance), "lowest_balance_with": s.money(with.LowestProjectedBalance),
			"ending_balance_without": s.money(base.EndingProjectedBalance), "ending_balance_with": s.money(with.EndingProjectedBalance),
			"safety_buffer": s.money(with.SafetyBuffer), "days_until_buffer_breach_with": with.DaysUntilSafetyBreach,
		}
	}
	res["assumptions"] = []string{
		"Balances come from recorded transactions only; unrecorded cash or bank activity is not included.",
		"The 30-day forecast projects recurring income, subscriptions and the user's recent daily spending pattern.",
	}
	return res
}

// ─── Draft actions ──────────────────────────────────────────────────────────

func (s *assistantSession) propose(c toolCall) any {
	a := &AssistantAction{}
	switch c.Name {
	case "propose_add_transaction":
		a.Kind = "add_transaction"
		a.Amount, a.Type, a.Category = argNum(c.Args, "amount"), argStr(c.Args, "type"), argStr(c.Args, "category")
		a.Note, a.Date, a.AccountID = argStr(c.Args, "note"), argStr(c.Args, "date"), argInt(c.Args, "account_id")
	case "propose_update_transaction":
		a.Kind = "update_transaction"
		a.TransactionID = argInt(c.Args, "transaction_id")
		a.Amount, a.Type, a.Category = argNum(c.Args, "amount"), argStr(c.Args, "type"), argStr(c.Args, "category")
		a.Note, a.Date = argStr(c.Args, "note"), argStr(c.Args, "date")
	case "propose_delete_transaction":
		a.Kind, a.TransactionID = "delete_transaction", argInt(c.Args, "transaction_id")
	case "propose_set_budget":
		a.Kind, a.Category, a.Amount = "set_budget", argStr(c.Args, "category"), argNum(c.Args, "monthly_limit")
	case "propose_goal_contribution":
		a.Kind = "goal_deposit"
		if strings.EqualFold(argStr(c.Args, "direction"), "withdraw") {
			a.Kind = "goal_withdraw"
		}
		a.GoalID, a.Amount = argInt(c.Args, "goal_id"), argNum(c.Args, "amount")
	case "propose_debt_payment":
		a.Kind, a.DebtID, a.Amount = "debt_payment", argInt(c.Args, "debt_id"), argNum(c.Args, "amount")
	default:
		return map[string]any{"error": "unknown tool " + c.Name}
	}
	draft, err := s.app.normalizeAssistantAction(s.username, a)
	if err != nil {
		return map[string]any{"error": err.Error(), "saved": false}
	}
	s.draft = draft
	return map[string]any{
		"status":  "draft shown to the user with Confirm and Cancel buttons; NOT saved",
		"summary": draft.Summary, "details": draft.Details, "saved": false,
	}
}

func (app *App) findUserTransaction(username string, id int) (*Transaction, bool) {
	txs, _ := app.DB.GetTransactions(username)
	for i := range txs {
		if txs[i].ID == id {
			return &txs[i], true
		}
	}
	return nil, false
}

func (app *App) validCategory(username, category string) (string, bool) {
	category = strings.ToLower(strings.TrimSpace(category))
	cats, _ := app.DB.GetCategories(username)
	for _, c := range cats {
		if c.Slug == category {
			return category, true
		}
	}
	return "other", false
}

// normalizeAssistantAction validates a proposed action against the user's own
// records and builds the summary shown on the confirmation card. It runs both
// when the model proposes and again at confirmation, since the client could
// send anything back.
func (app *App) normalizeAssistantAction(username string, a *AssistantAction) (*AssistantAction, error) {
	if a == nil {
		return nil, fmt.Errorf("missing action")
	}
	curr := app.DB.GetCurrency(username)
	money := func(v float64) string { return formatMoney(v, curr) }
	out := AssistantAction{Kind: strings.TrimSpace(a.Kind)}
	needAmount := func() error {
		if !validMoney(a.Amount) || a.Amount <= 0 {
			return fmt.Errorf("amount must be a positive number")
		}
		out.Amount = a.Amount
		return nil
	}

	switch out.Kind {
	case "add_transaction":
		if err := needAmount(); err != nil {
			return nil, err
		}
		out.Type = strings.ToLower(strings.TrimSpace(a.Type))
		if out.Type != "income" {
			out.Type = "expense"
		}
		out.Category, _ = app.validCategory(username, a.Category)
		out.Note = truncateRunes(strings.TrimSpace(a.Note), 120)
		if out.Note == "" {
			out.Note = "Assistant entry"
		}
		out.Date = dayOrDefault(a.Date, time.Now()).Format("2006-01-02")
		accounts, _ := app.DB.GetAccounts(username)
		accountName := ""
		for _, acc := range accounts {
			if acc.IsDefault && out.AccountID == 0 {
				out.AccountID, accountName = acc.ID, acc.Name
			}
		}
		for _, acc := range accounts {
			if a.AccountID != 0 && acc.ID == a.AccountID {
				out.AccountID, accountName = acc.ID, acc.Name
			}
		}
		out.Summary = fmt.Sprintf("Add %s of %s", out.Type, money(out.Amount))
		out.Details = []string{"Category: " + out.Category, "Note: " + out.Note, "Date: " + out.Date}
		if accountName != "" {
			out.Details = append(out.Details, "Account: "+accountName)
		}

	case "update_transaction":
		t, ok := app.findUserTransaction(username, a.TransactionID)
		if !ok {
			return nil, fmt.Errorf("transaction %d not found", a.TransactionID)
		}
		out.TransactionID, out.AccountID = t.ID, t.AccountID
		out.Amount, out.Type, out.Category, out.Note, out.Date = t.Amount, t.Type, t.Category, t.Note, t.Date.Format("2006-01-02")
		var changes []string
		if a.Amount != 0 {
			if err := needAmount(); err != nil {
				return nil, err
			}
			if out.Amount != t.Amount {
				changes = append(changes, fmt.Sprintf("Amount: %s → %s", money(t.Amount), money(out.Amount)))
			}
		}
		if typ := strings.ToLower(strings.TrimSpace(a.Type)); (typ == "income" || typ == "expense") && typ != t.Type {
			out.Type = typ
			changes = append(changes, fmt.Sprintf("Type: %s → %s", t.Type, typ))
		}
		if strings.TrimSpace(a.Category) != "" {
			if cat, known := app.validCategory(username, a.Category); known && cat != t.Category {
				out.Category = cat
				changes = append(changes, fmt.Sprintf("Category: %s → %s", t.Category, cat))
			}
		}
		if note := truncateRunes(strings.TrimSpace(a.Note), 120); note != "" && note != t.Note {
			out.Note = note
			changes = append(changes, fmt.Sprintf("Note: %q → %q", t.Note, note))
		}
		if d := dayOrDefault(a.Date, time.Time{}); !d.IsZero() && d.Format("2006-01-02") != out.Date {
			changes = append(changes, fmt.Sprintf("Date: %s → %s", out.Date, d.Format("2006-01-02")))
			out.Date = d.Format("2006-01-02")
		}
		if len(changes) == 0 {
			return nil, fmt.Errorf("no changes to transaction %d", t.ID)
		}
		out.Summary = fmt.Sprintf("Edit %s of %s (%s, %s)", t.Type, money(t.Amount), t.Note, t.Date.Format("2006-01-02"))
		out.Details = changes

	case "delete_transaction":
		t, ok := app.findUserTransaction(username, a.TransactionID)
		if !ok {
			return nil, fmt.Errorf("transaction %d not found", a.TransactionID)
		}
		out.TransactionID, out.Danger = t.ID, true
		out.Summary = fmt.Sprintf("Delete %s of %s", t.Type, money(t.Amount))
		out.Details = []string{"Note: " + t.Note, "Category: " + t.Category, "Date: " + t.Date.Format("2006-01-02"), "This can't be undone."}

	case "set_budget":
		if err := needAmount(); err != nil {
			return nil, err
		}
		cat, known := app.validCategory(username, a.Category)
		if !known {
			return nil, fmt.Errorf("unknown category %q", a.Category)
		}
		out.Category = cat
		limits, _ := app.DB.GetBudgets(username)
		current := "none"
		if v, ok := limits[cat]; ok {
			current = money(v)
		}
		out.Summary = fmt.Sprintf("Set %s budget to %s per month", cat, money(out.Amount))
		out.Details = []string{fmt.Sprintf("Current limit: %s → New limit: %s", current, money(out.Amount))}

	case "goal_deposit", "goal_withdraw":
		if err := needAmount(); err != nil {
			return nil, err
		}
		g, _, err := app.DB.GetGoalByID(a.GoalID, username)
		if err != nil || g == nil {
			return nil, fmt.Errorf("goal %d not found", a.GoalID)
		}
		out.GoalID = g.ID
		after := g.SavedAmount + out.Amount
		verb := "Deposit"
		if out.Kind == "goal_withdraw" {
			if out.Amount > g.SavedAmount {
				return nil, fmt.Errorf("can't withdraw %s; only %s is saved in %s", money(out.Amount), money(g.SavedAmount), g.Name)
			}
			after, verb, out.Danger = g.SavedAmount-out.Amount, "Withdraw", true
		}
		out.Summary = fmt.Sprintf("%s %s %s goal \"%s\"", verb, money(out.Amount), map[bool]string{true: "from", false: "to"}[out.Kind == "goal_withdraw"], g.Name)
		out.Details = []string{fmt.Sprintf("Saved: %s → %s of %s", money(g.SavedAmount), money(after), money(g.TargetAmount))}

	case "debt_payment":
		if err := needAmount(); err != nil {
			return nil, err
		}
		d, err := app.DB.GetDebtByID(a.DebtID, username)
		if err != nil || d == nil || d.Status == "settled" {
			return nil, fmt.Errorf("open debt %d not found", a.DebtID)
		}
		if out.Amount > d.Remaining+0.005 {
			return nil, fmt.Errorf("payment %s is more than the remaining %s", money(out.Amount), money(d.Remaining))
		}
		out.DebtID = d.ID
		dir := "to"
		if d.Type == "owing_me" {
			dir = "from"
		}
		out.Summary = fmt.Sprintf("Record %s payment %s %s", money(out.Amount), dir, d.PersonName)
		out.Details = []string{fmt.Sprintf("Remaining: %s → %s", money(d.Remaining), money(d.Remaining-out.Amount)), "Also records a matching transaction."}

	default:
		return nil, fmt.Errorf("unsupported action %q", a.Kind)
	}
	return &out, nil
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// executeAssistantAction performs an already-normalized action.
func (app *App) executeAssistantAction(username string, a *AssistantAction) error {
	now := time.Now()
	atNow := func(day string) time.Time {
		d := dayOrDefault(day, now)
		return time.Date(d.Year(), d.Month(), d.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.Local)
	}
	switch a.Kind {
	case "add_transaction":
		return app.DB.AddTransactionFull(username, a.Amount, a.Category, a.Note, a.Type, "assistant", a.AccountID, atNow(a.Date))
	case "update_transaction":
		t, ok := app.findUserTransaction(username, a.TransactionID)
		if !ok {
			return fmt.Errorf("transaction not found")
		}
		updated, err := app.DB.UpdateTransactionFull(t.ID, username, a.Amount, a.Category, a.Note, a.Type, t.Tags, a.AccountID, atNow(a.Date))
		if err == nil && !updated {
			err = fmt.Errorf("transaction not found")
		}
		return err
	case "delete_transaction":
		deleted, err := app.DB.DeleteTransaction(a.TransactionID, username)
		if err == nil && !deleted {
			err = fmt.Errorf("transaction not found")
		}
		return err
	case "set_budget":
		return app.DB.SetBudget(username, a.Category, a.Amount)
	case "goal_deposit":
		_, err := app.DB.DepositToGoal(a.GoalID, username, a.Amount, "Via Spendly Assistant", false)
		return err
	case "goal_withdraw":
		_, err := app.DB.WithdrawFromGoal(a.GoalID, username, a.Amount, "Via Spendly Assistant", false)
		return err
	case "debt_payment":
		_, err := app.DB.RecordDebtPayment(a.DebtID, username, a.Amount, now, true)
		return err
	}
	return fmt.Errorf("unsupported action")
}
