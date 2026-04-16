package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// New mints a session id of the form "20260416-153012-a3f4c8".
func New() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session id entropy: %w", err)
	}
	return fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(b[:])), nil
}

func latestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".crag", "last-session"), nil
}

// RecordLatest persists id at ~/.crag/last-session so status/logs can default
// to it when the user omits the explicit session argument.
func RecordLatest(id string) error {
	path, err := latestPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id), 0o644)
}

// Latest returns the most recently recorded session id, or "" if none exists.
func Latest() (string, error) {
	path, err := latestPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
