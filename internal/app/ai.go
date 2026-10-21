package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParsedAITransaction represents the extracted financial transaction details.
type ParsedAITransaction struct {
	Note           string   `json:"note"`
	Amount         float64  `json:"amount"`
	Category       string   `json:"category"`
	Type           string   `json:"type"` // "expense", "income", "transfer"
	Date           string   `json:"date"` // YYYY-MM-DD
	AccountID      int      `json:"account_id"`
	AccountName    string   `json:"account_name"`
	Tags           string   `json:"tags"`
	Confidence     float64  `json:"confidence"`
	Source         string   `json:"source"` // "local_nlp", "gemini", "openai"
	RawText        string   `json:"raw_text"`
	SuggestedItems []string `json:"suggested_items,omitempty"`
}

// AISettings stores the user's AI assistant configuration.
type AISettings struct {
	Username string `json:"username"`
	Provider string `json:"provider"` // "hybrid", "gemini", "openai", "local"
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	AutoSave bool   `json:"auto_save"`
}

// ParseAITransaction parses natural language text using the configured AI engine, with instant local fallback.
func ParseAITransaction(text string, accounts []Account, settings *AISettings) ParsedAITransaction {
	text = strings.TrimSpace(text)
	if text == "" {
		todayStr := time.Now().Format("2006-01-02")
		return ParsedAITransaction{
			Note:        "",
			Amount:      0,
			Category:    "other",
			Type:        "expense",
			Date:        todayStr,
			Confidence:  0.0,
			Source:      "empty",
			RawText:     text,
		}
	}

	provider := "hybrid"
	apiKey := ""
	model := ""
	if settings != nil {
		if settings.Provider != "" {
			provider = strings.ToLower(settings.Provider)
		}
		apiKey = strings.TrimSpace(settings.APIKey)
		model = strings.TrimSpace(settings.Model)
	}

	// 1. If explicit Gemini provider and key provided
	if (provider == "gemini" || (provider == "hybrid" && strings.HasPrefix(apiKey, "AIza"))) && apiKey != "" {
		if parsed, err := parseWithGemini(apiKey, model, text, accounts); err == nil && parsed != nil && parsed.Amount > 0 {
			parsed.Source = "gemini"
			parsed.RawText = text
			return *parsed
		}
	}

	// 2. If explicit OpenAI provider and key provided
	if (provider == "openai" || (provider == "hybrid" && strings.HasPrefix(apiKey, "sk-"))) && apiKey != "" {
		if parsed, err := parseWithOpenAI(apiKey, model, text, accounts); err == nil && parsed != nil && parsed.Amount > 0 {
			parsed.Source = "openai"
			parsed.RawText = text
			return *parsed
		}
	}

	// 3. Built-in Local Smart NLP Engine (Out of the box, zero external dependency, fast & reliable)
	localParsed := parseWithLocalNLP(text, accounts)
	localParsed.RawText = text
	return localParsed
}

// ─── Gemini Flash API Integration ──────────────────────────────────────────

func parseWithGemini(apiKey, model, text string, accounts []Account) (*ParsedAITransaction, error) {
	if model == "" {
		model = "gemini-3.5-flash-lite"
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	todayStr := time.Now().Format("2006-01-02")
	dayOfWeek := time.Now().Weekday().String()

	var accountNames []string
	for _, acc := range accounts {
		accountNames = append(accountNames, fmt.Sprintf("%s (ID: %d)", acc.Name, acc.ID))
	}
	accountsContext := strings.Join(accountNames, ", ")
	if accountsContext == "" {
		accountsContext = "Main Bank (ID: 0)"
	}

	prompt := fmt.Sprintf(`You are a financial transaction natural language parser for the Spendly budget app.
Today is %s (%s).
Allowed Categories: ["food", "transport", "housing", "entertainment", "shopping", "healthcare", "education", "bills", "personal", "other"].
Allowed Types: ["expense", "income", "transfer"].
Available Accounts: [%s].

Extract the transaction details from this text or bank SMS:
"%s"

Return strictly valid JSON only without markdown code blocks, matching this schema:
{
  "note": "Clean merchant, payee, or description (e.g. Dinner at KFC, Uber ride, Salary)",
  "amount": 4500.0,
  "category": "food",
  "type": "expense",
  "date": "YYYY-MM-DD",
  "account_id": 0,
  "account_name": "Main Bank",
  "confidence": 0.95
}`, todayStr, dayOfWeek, accountsContext, text)

	reqPayload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"temperature":      0.1,
		},
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini API status %d: %s", resp.StatusCode, string(body))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, err
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty gemini response")
	}

	replyText := geminiResp.Candidates[0].Content.Parts[0].Text
	replyText = strings.TrimPrefix(strings.TrimSpace(replyText), "```json")
	replyText = strings.TrimPrefix(replyText, "```")
	replyText = strings.TrimSuffix(replyText, "```")
	replyText = strings.TrimSpace(replyText)

	var parsed ParsedAITransaction
	if err := json.Unmarshal([]byte(replyText), &parsed); err != nil {
		return nil, err
	}

	if parsed.Date == "" {
		parsed.Date = todayStr
	}
	if parsed.Category == "" {
		parsed.Category = "other"
	}
	if parsed.Type == "" {
		parsed.Type = "expense"
	}
	if parsed.Confidence == 0 {
		parsed.Confidence = 0.95
	}

	return &parsed, nil
}

// ─── OpenAI API Integration ────────────────────────────────────────────────

func parseWithOpenAI(apiKey, model, text string, accounts []Account) (*ParsedAITransaction, error) {
	if model == "" {
		model = "gpt-4o-mini"
	}

	url := "https://api.openai.com/v1/chat/completions"

	todayStr := time.Now().Format("2006-01-02")
	dayOfWeek := time.Now().Weekday().String()

	var accountNames []string
	for _, acc := range accounts {
		accountNames = append(accountNames, fmt.Sprintf("%s (ID: %d)", acc.Name, acc.ID))
	}
	accountsContext := strings.Join(accountNames, ", ")
	if accountsContext == "" {
		accountsContext = "Main Bank (ID: 0)"
	}

	sysPrompt := fmt.Sprintf(`You are a financial transaction natural language parser for the Spendly app.
Today is %s (%s).
Allowed Categories: ["food", "transport", "housing", "entertainment", "shopping", "healthcare", "education", "bills", "personal", "other"].
Allowed Types: ["expense", "income", "transfer"].
Available Accounts: [%s].
Return valid JSON only matching schema:
{
  "note": "Clean merchant or description",
  "amount": 4500.0,
  "category": "food",
  "type": "expense",
  "date": "YYYY-MM-DD",
  "account_id": 0,
  "account_name": "Main Bank",
  "confidence": 0.95
}`, todayStr, dayOfWeek, accountsContext)

	reqPayload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": sysPrompt},
			{"role": "user", "content": text},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0.1,
	}

	payloadBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai API status %d: %s", resp.StatusCode, string(body))
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, err
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("empty openai choices")
	}

	var parsed ParsedAITransaction
	if err := json.Unmarshal([]byte(openAIResp.Choices[0].Message.Content), &parsed); err != nil {
		return nil, err
	}

	if parsed.Date == "" {
		parsed.Date = todayStr
	}
	if parsed.Category == "" {
		parsed.Category = "other"
	}
	if parsed.Type == "" {
		parsed.Type = "expense"
	}
	if parsed.Confidence == 0 {
		parsed.Confidence = 0.95
	}

	return &parsed, nil
}

// ─── Local Smart NLP Rule & Heuristic Parser ───────────────────────────────

func parseWithLocalNLP(text string, accounts []Account) ParsedAITransaction {
	rawLower := strings.ToLower(text)
	now := time.Now()
	todayStr := now.Format("2006-01-02")

	// 1. Check for Bank Alert / SMS Format
	if isBankSMS(rawLower) {
		smsParsed := parseBankSMS(text, rawLower, now, accounts)
		if smsParsed.Amount > 0 {
			smsParsed.Source = "local_nlp (bank_sms)"
			return smsParsed
		}
	}

	// 2. Transaction Type Detection
	txnType := "expense"
	if containsAny(rawLower, "received", "credited", "credit alert", "deposit", "salary", "earned", "allowance", "refund", "cashback", "dividend", "interest", "inflow") {
		txnType = "income"
	} else if containsAny(rawLower, "transfer to", "transferred to", "sent to", "moved to", "withdrawal", "withdrew") {
		txnType = "transfer"
	}

	// 3. Date Detection
	txDate := todayStr
	if strings.Contains(rawLower, "yesterday") {
		txDate = now.AddDate(0, 0, -1).Format("2006-01-02")
	} else if strings.Contains(rawLower, "day before yesterday") {
		txDate = now.AddDate(0, 0, -2).Format("2006-01-02")
	} else if strings.Contains(rawLower, "tomorrow") {
		txDate = now.AddDate(0, 0, 1).Format("2006-01-02")
	} else {
		// Look for explicit date formats (e.g., 2026-10-20, 20/10/2026, 20-Oct-2026, Oct 20)
		if parsedDate, ok := extractExplicitDate(text, now); ok {
			txDate = parsedDate
		}
	}

	// 4. Amount Extraction
	amount, amountRawStr := extractAmount(text)

	// 5. Category Detection
	category, catConfidence := detectCategory(rawLower)

	// 6. Account Matching
	accountID := 0
	accountName := "Main Bank"
	if len(accounts) > 0 {
		accountID = accounts[0].ID
		accountName = accounts[0].Name
	}
	for _, acc := range accounts {
		accLower := strings.ToLower(acc.Name)
		if accLower != "" && strings.Contains(rawLower, accLower) {
			accountID = acc.ID
			accountName = acc.Name
			break
		}
	}

	// 7. Clean Note / Description Extraction
	note := extractCleanNote(text, amountRawStr, rawLower, category)

	confidence := 0.85
	if amount > 0 && note != "" && catConfidence > 0.7 {
		confidence = 0.95
	} else if amount <= 0 {
		confidence = 0.40
	}

	return ParsedAITransaction{
		Note:        note,
		Amount:      amount,
		Category:    category,
		Type:        txnType,
		Date:        txDate,
		AccountID:   accountID,
		AccountName: accountName,
		Confidence:  confidence,
		Source:      "local_nlp",
		RawText:     text,
	}
}

// ─── Helper Functions ──────────────────────────────────────────────────────

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func isBankSMS(lower string) bool {
	matches := 0
	for _, kw := range []string{"acct", "txn", "dr:", "cr:", "amt", "bal", "pos purchase", "web purchase", "debit alert", "credit alert", "desc:"} {
		if strings.Contains(lower, kw) {
			matches++
		}
	}
	return matches >= 2
}

func parseBankSMS(text, lower string, now time.Time, accounts []Account) ParsedAITransaction {
	txnType := "expense"
	if containsAny(lower, "credit alert", "credited", "cr:", "deposit") {
		txnType = "income"
	}

	// Amount extraction from SMS: "Amt: NGN 15,000.00" or "Amt: 15,000.00"
	amtRe := regexp.MustCompile(`(?i)(?:amt|amount|ngn|usd|eur|gbp|[₦$€£])\s*:?\s*([0-9,]+(?:\.[0-9]{1,2})?)`)
	var amount float64
	if m := amtRe.FindStringSubmatch(text); len(m) > 1 {
		cleaned := strings.ReplaceAll(m[1], ",", "")
		amount, _ = strconv.ParseFloat(cleaned, 64)
	}
	if amount == 0 {
		amount, _ = extractAmount(text)
	}

	// Note / Description: "Desc: POS/CHICKEN REPUBLIC" or "Desc: CHICKEN REPUBLIC"
	descRe := regexp.MustCompile(`(?i)(?:desc|details|narr|narration|merchant)\s*:?\s*([^;|\n\r]+)`)
	note := ""
	if m := descRe.FindStringSubmatch(text); len(m) > 1 {
		clean := strings.TrimSpace(m[1])
		clean = regexp.MustCompile(`(?i)^(pos|web|atm|nip|trf|ft)\s*[/_-]?\s*`).ReplaceAllString(clean, "")
		note = clean
	}
	if note == "" {
		note = "Bank Transaction"
	}

	category, _ := detectCategory(strings.ToLower(note + " " + text))

	accountID := 0
	accountName := "Main Bank"
	if len(accounts) > 0 {
		accountID = accounts[0].ID
		accountName = accounts[0].Name
	}
	for _, acc := range accounts {
		accLower := strings.ToLower(acc.Name)
		if accLower != "" && strings.Contains(lower, accLower) {
			accountID = acc.ID
			accountName = acc.Name
			break
		}
	}

	txDate := now.Format("2006-01-02")
	if parsedDate, ok := extractExplicitDate(text, now); ok {
		txDate = parsedDate
	}

	return ParsedAITransaction{
		Note:        capitalizeTitle(note),
		Amount:      amount,
		Category:    category,
		Type:        txnType,
		Date:        txDate,
		AccountID:   accountID,
		AccountName: accountName,
		Confidence:  0.92,
		Source:      "local_nlp",
		RawText:     text,
	}
}

func extractAmount(text string) (float64, string) {
	// Look for multipliers like 50k, 1.5m
	kmRe := regexp.MustCompile(`(?i)(?:[₦$€£]|NGN|USD|EUR|GBP)?\s*([0-9]+(?:\.[0-9]+)?)\s*([km])\b`)
	if m := kmRe.FindStringSubmatch(text); len(m) > 2 {
		val, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			unit := strings.ToLower(m[2])
			if unit == "k" {
				return val * 1000, m[0]
			} else if unit == "m" {
				return val * 1000000, m[0]
			}
		}
	}

	// Look for standard numbers with currency prefix or standalone formatted numbers
	// Example: ₦4,500.50, $250, 15,000, 3200
	currAmtRe := regexp.MustCompile(`(?:[₦$€£]|NGN|USD|EUR|GBP)\s*([0-9]{1,3}(?:,[0-9]{3})*(?:\.[0-9]{1,2})?|[0-9]+(?:\.[0-9]{1,2})?)`)
	if m := currAmtRe.FindStringSubmatch(text); len(m) > 1 {
		cleaned := strings.ReplaceAll(m[1], ",", "")
		if val, err := strconv.ParseFloat(cleaned, 64); err == nil && val > 0 {
			return val, m[0]
		}
	}

	// Fallback to standalone numbers, avoiding 4-digit years like 2024, 2025, 2026
	numRe := regexp.MustCompile(`\b([0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]{1,2})?|[0-9]+(?:\.[0-9]{1,2})?)\b`)
	matches := numRe.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) > 1 {
			cleaned := strings.ReplaceAll(match[1], ",", "")
			if val, err := strconv.ParseFloat(cleaned, 64); err == nil {
				// Skip if likely a 4-digit year near current time
				if val >= 2020 && val <= 2035 && !strings.Contains(match[1], ".") && !strings.Contains(match[1], ",") {
					continue
				}
				if val > 0 {
					return val, match[0]
				}
			}
		}
	}

	return 0, ""
}

func detectCategory(lower string) (string, float64) {
	categories := []struct {
		Category string
		Keywords []string
	}{
		{
			"food",
			[]string{
				"kfc", "mcdonald", "starbucks", "domino", "subway", "chicken republic", "the place",
				"kilimanjaro", "sweet sensation", "cold stone", "mr biggs", "buka", "burger", "pizza",
				"shawarma", "suya", "restaurant", "cafe", "coffee", "breakfast", "lunch", "dinner",
				"snack", "groceries", "food", "eat", "dining", "beer", "wine", "drinks", "drink", "cocktail",
				"pastry", "bakery", "potatoes", "rice", "beans",
			},
		},
		{
			"transport",
			[]string{
				"uber", "bolt", "lyft", "taxi", "cab", "bus", "train", "flight", "airline", "airfare",
				"fuel", "petrol", "gas", "diesel", "car wash", "parking", "toll", "mechanic", "shell",
				"total", "chevron", "oando", "conoil", "ardova", "ride", "fare", "danfo", "brt",
			},
		},
		{
			"housing",
			[]string{
				"rent", "landlord", "mortgage", "apartment", "plumber", "plumbing", "furniture",
				"ikea", "home depot", "decor", "realtor", "maintenance", "service charge", "renovation",
			},
		},
		{
			"bills",
			[]string{
				"electricity", "nepa", "phcn", "ekedc", "ikedc", "water bill", "internet", "wifi",
				"data", "airtime", "mtn", "airtel", "glo", "9mobile", "starlink", "smile", "spectranet",
				"utility", "recharge", "bill",
			},
		},
		{
			"entertainment",
			[]string{
				"netflix", "spotify", "apple music", "cinema", "movie", "film", "game", "steam",
				"playstation", "xbox", "youtube", "dstv", "gotv", "showmax", "prime video", "concert",
				"ticket", "club", "party", "amusement", "bowling",
			},
		},
		{
			"shopping",
			[]string{
				"shopping", "supermarket", "market", "cloth", "clothes", "shoe", "shoes", "bag",
				"amazon", "jumia", "konga", "aliexpress", "walmart", "target", "shoprite", "spar",
				"hubmart", "boutique", "gadget", "phone", "laptop", "store", "buy", "bought",
			},
		},
		{
			"healthcare",
			[]string{
				"pharmacy", "drugs", "medicine", "hospital", "doctor", "dentist", "clinic", "medication",
				"cvs", "walgreens", "medplus", "healthplus", "lab test", "checkup", "gym", "fitness",
				"optician", "physio", "therapy",
			},
		},
		{
			"education",
			[]string{
				"tuition", "school fee", "course", "udemy", "coursera", "book", "books", "exam",
				"training", "cert", "university", "college", "school", "lecture",
			},
		},
		{
			"personal",
			[]string{
				"haircut", "salon", "barber", "spa", "massage", "nails", "skincare", "perfume",
				"cosmetics", "gift", "charity", "tithe", "offering", "donation",
			},
		},
	}

	for _, item := range categories {
		for _, kw := range item.Keywords {
			if strings.Contains(lower, kw) {
				return item.Category, 0.90
			}
		}
	}

	return "other", 0.50
}

func extractExplicitDate(text string, now time.Time) (string, bool) {
	// ISO format: 2026-10-20
	isoRe := regexp.MustCompile(`\b(20[2-3][0-9]-[0-1][0-9]-[0-3][0-9])\b`)
	if m := isoRe.FindStringSubmatch(text); len(m) > 1 {
		return m[1], true
	}

	// 20/10/2026 or 20-10-2026
	slashRe := regexp.MustCompile(`\b([0-3]?[0-9])[-/.]([0-1]?[0-9])[-/.](20[2-3][0-9])\b`)
	if m := slashRe.FindStringSubmatch(text); len(m) > 3 {
		d, _ := strconv.Atoi(m[1])
		mon, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if d >= 1 && d <= 31 && mon >= 1 && mon <= 12 {
			return fmt.Sprintf("%04d-%02d-%02d", y, mon, d), true
		}
	}

	// 20-Oct-2026 or 20 Oct 2026
	textMonRe := regexp.MustCompile(`(?i)\b([0-3]?[0-9])[-/ ]?(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*[-/ ]?(20[2-3][0-9])?\b`)
	if m := textMonRe.FindStringSubmatch(text); len(m) > 2 {
		d, _ := strconv.Atoi(m[1])
		monStr := strings.ToLower(m[2])
		months := map[string]int{
			"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
			"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
		}
		mon := months[monStr]
		y := now.Year()
		if len(m) > 3 && m[3] != "" {
			y, _ = strconv.Atoi(m[3])
		}
		if d >= 1 && d <= 31 && mon >= 1 && mon <= 12 {
			return fmt.Sprintf("%04d-%02d-%02d", y, mon, d), true
		}
	}

	return "", false
}

func extractCleanNote(text, amountRaw, lower, category string) string {
	clean := text

	// Remove amount string
	if amountRaw != "" {
		clean = strings.Replace(clean, amountRaw, " ", 1)
	}

	// Remove common currency symbols
	clean = regexp.MustCompile(`(?i)\b(ngn|usd|eur|gbp)\b|[₦$€£]`).ReplaceAllString(clean, " ")

	// Remove relative date terms
	clean = regexp.MustCompile(`(?i)\b(yesterday|today|tomorrow|last\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b`).ReplaceAllString(clean, " ")

	// Remove filler prefixes
	clean = regexp.MustCompile(`(?i)^\s*(i\s+)?(spent|paid\s+for|paid|bought|got|received|transferred|ordered|withdrew|deposited)\s+`).ReplaceAllString(clean, "")

	// Remove trailing/leading prepositions
	clean = regexp.MustCompile(`(?i)\s+(with|via|using|from|through)\s+(my\s+)?[a-z0-9\s_-]+$`).ReplaceAllString(clean, "")
	clean = regexp.MustCompile(`(?i)^\s*(on|for|at|to)\s+`).ReplaceAllString(clean, "")

	clean = strings.TrimSpace(clean)
	clean = regexp.MustCompile(`\s+`).ReplaceAllString(clean, " ")

	if clean == "" {
		switch category {
		case "food":
			return "Food & Dining"
		case "transport":
			return "Transportation"
		case "housing":
			return "Housing / Rent"
		case "bills":
			return "Utility Bill"
		case "shopping":
			return "Shopping Purchase"
		case "entertainment":
			return "Entertainment"
		case "healthcare":
			return "Healthcare"
		case "education":
			return "Education"
		case "personal":
			return "Personal Care"
		default:
			return "General Transaction"
		}
	}

	return capitalizeTitle(clean)
}

func capitalizeTitle(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		// If already upper (like KFC, POS), keep it
		if w == strings.ToUpper(w) && len(w) <= 4 {
			continue
		}
		lower := strings.ToLower(w)
		if i > 0 && (lower == "at" || lower == "in" || lower == "on" || lower == "of" || lower == "for" || lower == "and" || lower == "to" || lower == "the" || lower == "a") {
			words[i] = lower
		} else {
			words[i] = strings.ToUpper(lower[:1]) + lower[1:]
		}
	}
	return strings.Join(words, " ")
}
