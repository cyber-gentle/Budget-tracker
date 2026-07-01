package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// User holds account credentials and their tracker file path.
type User struct {
	Username   string    `json:"username"`
	Password   string    `json:"password"` // plaintext for simplicity; hash in production
	CreatedAt  time.Time `json:"created_at"`
	TrackerDir string    `json:"-"`
}

// Session represents an active login session.
type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthStore manages users and sessions.
type AuthStore struct {
	mu       sync.RWMutex
	users    map[string]*User
	sessions map[string]*Session
	filePath string
}

// NewAuthStore creates an auth store from a JSON file or fresh.
func NewAuthStore(filePath string) *AuthStore {
	as := &AuthStore{
		users:    make(map[string]*User),
		sessions: make(map[string]*Session),
		filePath: filePath,
	}
	as.load()
	return as
}

func (as *AuthStore) load() {
	data, err := os.ReadFile(as.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", as.filePath, err)
		return
	}
	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", as.filePath, err)
		return
	}
	for _, u := range users {
		as.users[u.Username] = u
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

// Signup creates a new user account.
func (as *AuthStore) Signup(username, password string) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	if _, exists := as.users[username]; exists {
		return fmt.Errorf("username already taken")
	}
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}

	u := &User{
		Username:  username,
		Password:  password,
		CreatedAt: time.Now(),
	}
	as.users[username] = u
	return as.save()
}

// Login verifies credentials and creates a session. Returns the session token.
func (as *AuthStore) Login(username, password string) (string, error) {
	as.mu.RLock()
	u, exists := as.users[username]
	as.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("invalid username or password")
	}
	if u.Password != password {
		return "", fmt.Errorf("invalid username or password")
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}

	s := &Session{
		Token:     token,
		Username:  username,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	as.mu.Lock()
	as.sessions[token] = s
	as.mu.Unlock()

	return token, nil
}

// ValidateSession checks if a session token is valid and returns the username.
func (as *AuthStore) ValidateSession(token string) (string, bool) {
	as.mu.RLock()
	s, exists := as.sessions[token]
	as.mu.RUnlock()

	if !exists || time.Now().After(s.ExpiresAt) {
		if exists {
			as.mu.Lock()
			delete(as.sessions, token)
			as.mu.Unlock()
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
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
