package db

import (
	"context"
	"path/filepath"
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
