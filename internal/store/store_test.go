package store

import (
	"context"
	"os"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run store tests against Postgres")
	}
	st, err := Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.db.ExecContext(context.Background(), `TRUNCATE ingredient_stock, guest, guest_pref`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSetListStatuses(t *testing.T) {
	st := testStore(t)
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

func TestGuestStore(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if err := st.AddPreference(ctx, "Anna", "allergy", "nuts"); err != nil {
		t.Fatalf("add allergy: %v", err)
	}
	if err := st.AddPreference(ctx, "anna", "like", "mezcal"); err != nil { // same guest, case-insensitive
		t.Fatalf("add like: %v", err)
	}

	g, err := st.GetGuest(ctx, "ANNA")
	if err != nil {
		t.Fatalf("get guest: %v", err)
	}
	if g == nil {
		t.Fatal("expected guest, got nil")
	}
	if len(g.Allergies) != 1 || g.Allergies[0] != "nuts" || len(g.Likes) != 1 || g.Likes[0] != "mezcal" {
		t.Fatalf("unexpected guest: %+v", g)
	}
}
