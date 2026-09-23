package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// User holds account credentials.
type User struct {
	Username  string    `json:"username"`
	Password  string    `json:"password"` // "sha256:<salt>:<hash>" or plaintext legacy
	CreatedAt time.Time `json:"created_at"`
}

// Session represents an active login session.
type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthStore manages users and sessions with persistence.
type AuthStore struct {
	mu           sync.RWMutex
	users        map[string]*User
	sessions     map[string]*Session
	filePath     string
	sessionsPath string
}

// NewAuthStore creates an auth store from a JSON file or fresh.
func NewAuthStore(filePath string) *AuthStore {
	as := &AuthStore{
		users:        make(map[string]*User),
		sessions:     make(map[string]*Session),
		filePath:     filePath,
		sessionsPath: filepath.Join(filepath.Dir(filePath), "sessions.json"),
	}
	as.load()
	return as
}

func (as *AuthStore) load() {
	// Load users
	data, err := os.ReadFile(as.filePath)
	if err == nil {
		var users []*User
		if err := json.Unmarshal(data, &users); err == nil {
			for _, u := range users {
				as.users[u.Username] = u
			}
		}
	}

	// Load persistent sessions
	sData, err := os.ReadFile(as.sessionsPath)
	if err == nil {
		var sessions []*Session
		if err := json.Unmarshal(sData, &sessions); err == nil {
			now := time.Now()
			for _, s := range sessions {
				if s.ExpiresAt.After(now) {
					as.sessions[s.Token] = s
				}
			}
		}
	}
}

func (as *AuthStore) save() error {
	users := make([]*User, 0, len(as.users))
	for _, u := range as.users {
		users = append(users, u)
	}

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(as.filePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(as.filePath, data, 0644)
}

func (as *AuthStore) saveSessionsLocked() error {
	sessions := make([]*Session, 0, len(as.sessions))
	now := time.Now()
	for _, s := range as.sessions {
		if s.ExpiresAt.After(now) {
			sessions = append(sessions, s)
		}
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(as.sessionsPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(as.sessionsPath, data, 0644)
}

// Signup creates a new user account with hashed password.
func (as *AuthStore) Signup(username, password string) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if _, exists := as.users[username]; exists {
		return fmt.Errorf("username already taken")
	}
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}

	hashed, err := createPasswordHash(password)
	if err != nil {
		return fmt.Errorf("security error: %v", err)
	}

	u := &User{
		Username:  username,
		Password:  hashed,
		CreatedAt: time.Now(),
	}
	as.users[username] = u
	return as.save()
}

// Login verifies credentials and creates a persistent session.
func (as *AuthStore) Login(username, password string) (string, error) {
	as.mu.Lock()
	defer as.mu.Unlock()

	u, exists := as.users[username]
	if !exists {
		return "", fmt.Errorf("invalid username or password")
	}

	matched, needsUpgrade := verifyPassword(u.Password, password)
	if !matched {
		return "", fmt.Errorf("invalid username or password")
	}

	if needsUpgrade {
		if newHash, err := createPasswordHash(password); err == nil {
			u.Password = newHash
			_ = as.save()
		}
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}

	s := &Session{
		Token:     token,
		Username:  username,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7-day session
	}

	as.sessions[token] = s
	_ = as.saveSessionsLocked()

	return token, nil
}

// ValidateSession checks if a session token is valid and returns the username.
func (as *AuthStore) ValidateSession(token string) (string, bool) {
	as.mu.Lock()
	defer as.mu.Unlock()

	s, exists := as.sessions[token]
	if !exists || time.Now().After(s.ExpiresAt) {
		if exists {
			delete(as.sessions, token)
			_ = as.saveSessionsLocked()
		}
		return "", false
	}
	return s.Username, true
}

// Logout invalidates a session.
func (as *AuthStore) Logout(token string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	delete(as.sessions, token)
	_ = as.saveSessionsLocked()
}

// ─── Cryptographic Helpers ──────────────────────────────────────────────────

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func createPasswordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	saltHex := hex.EncodeToString(salt)
	hash := sha256Hash(password, saltHex)
	return fmt.Sprintf("sha256:%s:%s", saltHex, hash), nil
}

func sha256Hash(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}

// verifyPassword checks password match and returns true if an upgrade to hash format is needed.
func verifyPassword(storedPassword, inputPassword string) (bool, bool) {
	if strings.HasPrefix(storedPassword, "sha256:") {
		parts := strings.Split(storedPassword, ":")
		if len(parts) == 3 {
			salt := parts[1]
			expectedHash := parts[2]
			actualHash := sha256Hash(inputPassword, salt)
			return expectedHash == actualHash, false
		}
	}
	// Fallback check for legacy plaintext passwords
	if storedPassword == inputPassword {
		return true, true // matched legacy, needs upgrade
	}
	return false, false
}
