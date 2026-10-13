package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tfrydrychewicz/godir/internal/store"
	"github.com/tfrydrychewicz/godir/internal/store/storetest"
)

// TestSQLiteConformance runs the suite every Store implementation must pass
// against the SQLite one, each case on its own temporary database.
func TestSQLiteConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "godir.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() {
			if err := st.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
		return st
	})
}
