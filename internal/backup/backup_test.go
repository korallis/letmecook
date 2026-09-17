package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStubNeverCreatesBackup(t *testing.T) {
	target := filepath.Join(t.TempDir(), "not-created")
	if _, err := Create(context.Background(), nil, "", "", target); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := Verify(target); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("stub created state")
	}
}
