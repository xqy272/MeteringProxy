package hash

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
)

type Hasher struct {
	salt string
}

func New(saltFile string) (*Hasher, error) {
	// On Unix, reject salt files with group/other permissions.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(saltFile)
		if err != nil {
			return nil, fmt.Errorf("stat salt file: %w", err)
		}
		if mode := info.Mode(); mode&0077 != 0 {
			return nil, fmt.Errorf("salt file has insecure permissions %04o; expected 0600 (no group/other access)", mode.Perm())
		}
	} else {
		// On Windows, file permissions are managed via ACLs, not Unix mode bits.
		// Log a warning since we cannot enforce strict permissions.
		log.Printf("warning: salt file permissions check skipped on Windows; ensure %q is not world-readable", saltFile)
	}

	data, err := os.ReadFile(saltFile)
	if err != nil {
		return nil, fmt.Errorf("read salt file: %w", err)
	}
	return &Hasher{salt: string(data)}, nil
}

func NewWithSalt(salt string) *Hasher {
	return &Hasher{salt: salt}
}

func (h *Hasher) Hash(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(h.salt + value))
	return hex.EncodeToString(sum[:])
}

func (h *Hasher) HashBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(append([]byte(h.salt), data...))
	return hex.EncodeToString(sum[:])
}

func GenerateSalt() string {
	b := make([]byte, 32)
	if _, err := randRead(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var randRead = func(b []byte) (int, error) {
	return rand.Read(b)
}
