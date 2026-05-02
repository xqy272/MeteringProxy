package hash

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHashStableAndSalted(t *testing.T) {
	h1 := NewWithSalt("salt-a")
	h2 := NewWithSalt("salt-a")
	h3 := NewWithSalt("salt-b")

	got := h1.Hash("sk-test")
	if got == "" || got == "sk-test" {
		t.Fatalf("Hash returned unsafe value %q", got)
	}
	if got != h2.Hash("sk-test") {
		t.Fatal("same salt and value should produce the same hash")
	}
	if got == h3.Hash("sk-test") {
		t.Fatal("different salts should produce different hashes")
	}
	if h1.Hash("") != "" {
		t.Fatal("empty string should hash to empty string")
	}
}

func TestGenerateSalt(t *testing.T) {
	salt := GenerateSalt()
	if len(salt) != 64 {
		t.Fatalf("salt length = %d, want 64 hex chars", len(salt))
	}
	if strings.ToLower(salt) != salt {
		t.Fatal("salt should be lower-case hex")
	}
}

func TestNewRejectsInsecureSaltPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "salt")
	if err := os.WriteFile(path, []byte("test-salt"), 0644); err != nil {
		t.Fatalf("write salt: %v", err)
	}
	if _, err := New(path); err == nil {
		t.Fatal("expected insecure permission error")
	}
}

func TestNewReadsSecureSalt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "salt")
	if err := os.WriteFile(path, []byte("test-salt\n"), 0600); err != nil {
		t.Fatalf("write salt: %v", err)
	}
	h, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h.Hash("value") != NewWithSalt("test-salt\n").Hash("value") {
		t.Fatal("salt bytes should be used exactly as stored")
	}
}
