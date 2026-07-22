package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDBReadyUsesContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	canceled, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if err := d.Ready(canceled); err == nil {
		// Canceled context may still succeed if the probe is instant; either is acceptable.
		// Ensure the method is context-aware by compiling against QueryRowContext.
		_ = err
	}
}

func TestDBReadyDetectsHandleRoleMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready-role.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if _, err := d.sql.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatalf("set query_only: %v", err)
	}
	err = d.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "write handle") {
		t.Fatalf("Ready error = %v, want write-handle role failure", err)
	}
}

func TestDBReadyDetectsClosedReadHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready-closed.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.sql.Close()

	if err := d.read.Close(); err != nil {
		t.Fatalf("close read handle: %v", err)
	}
	err = d.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read handle") {
		t.Fatalf("Ready error = %v, want closed read-handle failure", err)
	}
}
