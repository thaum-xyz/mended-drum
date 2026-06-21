package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSetListStatuses(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if _, err := st.Set(ctx, "Gin", "in_stock"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Same normalised key, different casing/spacing -> updates in place.
	if _, err := st.Set(ctx, "  gin  ", "out"); err != nil {
		t.Fatalf("set update: %v", err)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
	if list[0].Status != Out {
		t.Fatalf("expected status out, got %s", list[0].Status)
	}

	statuses, err := st.Statuses(ctx)
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}
	if statuses["gin"] != Out {
		t.Fatalf("expected gin=out, got %q", statuses["gin"])
	}
}
