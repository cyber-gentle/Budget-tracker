package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func createPasswordHash(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func sha256Hash(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}

// verifyPassword checks password match using constant-time comparison to prevent timing attacks,
// and returns true if an upgrade to hash format is needed.
func verifyPassword(storedPassword, inputPassword string) (bool, bool) {
	if strings.HasPrefix(storedPassword, "$2a$") {
		err := bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(inputPassword))
		return err == nil, false
	}
	if strings.HasPrefix(storedPassword, "sha256:") {
		parts := strings.Split(storedPassword, ":")
		if len(parts) == 3 {
			salt := parts[1]
			expectedHash := parts[2]
			actualHash := sha256Hash(inputPassword, salt)
			return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actualHash)) == 1, true
		}
	}
	// Fallback check for legacy plaintext passwords
	if subtle.ConstantTimeCompare([]byte(storedPassword), []byte(inputPassword)) == 1 {
		return true, true // matched legacy, needs upgrade
	}
	return false, false
}
