package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s_%s_%d", prefix, hex.EncodeToString(b[:]), time.Now().UnixNano()%1e6)
}

func EnsureAgentID(dataDir, existing string) (string, error) {
	if existing != "" {
		return existing, nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, "agent.id")
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return id, nil
		}
	}
	host, _ := os.Hostname()
	id := NewID("agt_" + sanitize(host))
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "host"
	}
	return b.String()
}
