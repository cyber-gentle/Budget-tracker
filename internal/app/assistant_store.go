package app

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ─── Assistant memory & preferences storage ─────────────────────────────────
//
// Memories are short facts the user approved (goals, preferences, context).
// Users can list, edit and delete them; the assistant can only propose new ones,
// which are saved through the same confirmation flow as financial changes.

const (
	maxAssistantMemories   = 50
	maxAssistantMemoryChar = 300
)

type AssistantMemory struct {
	ID        int    `json:"id"`
	Content   string `json:"content"`
	Kind      string `json:"kind"` // goal, preference, context
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

var validMemoryKinds = map[string]bool{"goal": true, "preference": true, "context": true}

// Credentials and card numbers must never be stored (spec §3 and §5).
var (
	longDigitsRe   = regexp.MustCompile(`\d(?:[ -]?\d){11,}`)
	credentialWord = regexp.MustCompile(`(?i)\b(password|passcode|pin|otp|one[- ]time code|cvv|cvc|api[ _-]?key|bvn)\b`)
)

func (s *DBStore) migrateAssistant() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS assistant_memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		content TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'context',
		created_at DATETIME,
		updated_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_assistant_memories_user ON assistant_memories(username);

	CREATE TABLE IF NOT EXISTS assistant_prefs (
		username TEXT PRIMARY KEY,
		language TEXT NOT NULL DEFAULT 'en'
	);`)
	return err
}

// cleanMemory validates memory text and kind.
func cleanMemory(content, kind string) (string, string, error) {
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return "", "", errors.New("memory can't be empty")
	}
	if len([]rune(content)) > maxAssistantMemoryChar {
		return "", "", fmt.Errorf("memory must be %d characters or fewer", maxAssistantMemoryChar)
	}
	if longDigitsRe.MatchString(content) || credentialWord.MatchString(content) {
		return "", "", errors.New("memories can't contain card numbers, PINs, passwords, codes or keys")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validMemoryKinds[kind] {
		kind = "context"
	}
	return content, kind, nil
}

func (s *DBStore) GetAssistantMemories(username string) ([]AssistantMemory, error) {
	rows, err := s.db.Query("SELECT id, content, kind, created_at, updated_at FROM assistant_memories WHERE username = ? ORDER BY id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistantMemory{}
	for rows.Next() {
		var m AssistantMemory
		var created, updated time.Time
		if err := rows.Scan(&m.ID, &m.Content, &m.Kind, &created, &updated); err != nil {
			return nil, err
		}
		m.CreatedAt, m.UpdatedAt = created.Format("2006-01-02"), updated.Format("2006-01-02")
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *DBStore) AddAssistantMemory(username, content, kind string) (*AssistantMemory, error) {
	content, kind, err := cleanMemory(content, kind)
	if err != nil {
		return nil, err
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM assistant_memories WHERE username = ?", username).Scan(&count); err != nil {
		return nil, err
	}
	if count >= maxAssistantMemories {
		return nil, fmt.Errorf("you can keep up to %d memories; delete one first", maxAssistantMemories)
	}
	now := time.Now()
	res, err := s.db.Exec("INSERT INTO assistant_memories (username, content, kind, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		username, content, kind, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &AssistantMemory{ID: int(id), Content: content, Kind: kind, CreatedAt: now.Format("2006-01-02"), UpdatedAt: now.Format("2006-01-02")}, nil
}

func (s *DBStore) UpdateAssistantMemory(username string, id int, content, kind string) error {
	content, kind, err := cleanMemory(content, kind)
	if err != nil {
		return err
	}
	res, err := s.db.Exec("UPDATE assistant_memories SET content = ?, kind = ?, updated_at = ? WHERE id = ? AND username = ?",
		content, kind, time.Now(), id, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *DBStore) DeleteAssistantMemory(username string, id int) error {
	res, err := s.db.Exec("DELETE FROM assistant_memories WHERE id = ? AND username = ?", id, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *DBStore) DeleteAllAssistantMemories(username string) error {
	_, err := s.db.Exec("DELETE FROM assistant_memories WHERE username = ?", username)
	return err
}

// ─── Language preference ────────────────────────────────────────────────────

// assistantLanguages lists the reply languages offered. Gemini supports all of
// them, but quality varies; the prompt tells the model to say so honestly.
var assistantLanguages = map[string]string{
	"en": "English", "fr": "French", "es": "Spanish", "pt": "Portuguese", "de": "German",
	"ar": "Arabic", "sw": "Swahili", "zh": "Chinese (Simplified)", "hi": "Hindi",
	"ha": "Hausa", "yo": "Yoruba", "ig": "Igbo", "pcm": "Nigerian Pidgin",
}

func (s *DBStore) GetAssistantLanguage(username string) string {
	var lang string
	if err := s.db.QueryRow("SELECT language FROM assistant_prefs WHERE username = ?", username).Scan(&lang); err != nil {
		return "en"
	}
	if _, ok := assistantLanguages[lang]; !ok {
		return "en"
	}
	return lang
}

func (s *DBStore) SetAssistantLanguage(username, lang string) error {
	if _, ok := assistantLanguages[lang]; !ok {
		return fmt.Errorf("unsupported language %q", lang)
	}
	_, err := s.db.Exec(`INSERT INTO assistant_prefs (username, language) VALUES (?, ?)
		ON CONFLICT(username) DO UPDATE SET language = excluded.language`, username, lang)
	return err
}
