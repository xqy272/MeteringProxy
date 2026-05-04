package hash

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
)

// algorithm is a code-level lock on the hashing scheme. It is NOT embedded in
// hash output — that would constitute a second breaking change. It exists to
// make future maintainers pause before altering the algorithm, salt handling,
// or output format.
//
// The 2026-05 switch from SHA256(salt+value) to HMAC-SHA256(salt, value) was
// a one-time migration that broke historical API-key and client-IP groupings.
// Future changes REQUIRE a dual-read/write migration path in the database
// layer. The salt file, the HMAC-SHA256 algorithm, and the 64-char hex output
// format are locked.
const algorithm = "hmac-sha256-v1"

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
	mac := hmac.New(sha256.New, []byte(h.salt))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Hasher) HashBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(h.salt))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
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
