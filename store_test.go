// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/visvasity/kv"
	"github.com/visvasity/kvmemdb"
)

// newTestStore returns a Store and a fresh in-memory database for kv-backed
// tests. The identity domain is fixed for predictable identities.
func newTestStore(t *testing.T) (*Store, kv.Database) {
	t.Helper()
	s, err := New("/userdb", WithIdentityDomain("id.hostcheck.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	return s, kv.DatabaseFrom(kvmemdb.New())
}

// withRW runs fn inside a committed transaction, failing the test on any error.
func withRW(t *testing.T, db kv.Database, fn func(rw kv.ReadWriter) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.NewTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// withR runs fn against a snapshot, failing the test on any error.
func withR(t *testing.T, db kv.Database, fn func(r kv.Reader) error) {
	t.Helper()
	ctx := context.Background()
	snap, err := db.NewSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Discard(ctx)
	if err := fn(snap); err != nil {
		t.Fatal(err)
	}
}

// ----- Phase 2: persistence primitives & record round-trips -----

func TestKeyBuilders(t *testing.T) {
	s, _ := newTestStore(t)
	if got, want := s.emailKey("a@b.io"), "/userdb/e/a@b.io"; got != want {
		t.Errorf("emailKey = %q, want %q", got, want)
	}
	if got, want := s.identityKey("acct@id.hostcheck.invalid"), "/userdb/s/acct@id.hostcheck.invalid"; got != want {
		t.Errorf("identityKey = %q, want %q", got, want)
	}
}

func TestKeyspaceForms(t *testing.T) {
	// Non-absolute and trailing-slash keyspaces are accepted; a trailing "/" is
	// trimmed so keys are identical regardless of the trailing slash.
	for _, ks := range []string{"userdb", "userdb/"} {
		s, err := New(ks)
		if err != nil {
			t.Fatalf("New(%q): %v", ks, err)
		}
		if got, want := s.emailKey("a@b.io"), "userdb/e/a@b.io"; got != want {
			t.Errorf("New(%q) emailKey = %q, want %q", ks, got, want)
		}
	}
	if _, err := New(""); err == nil {
		t.Error("New(\"\") should fail")
	}
}

func TestEmailRecRoundTrip(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	in := &emailRec{
		Synthetic: "acct_1@id.hostcheck.invalid",
		Provider:  &Provider{Name: "google", Subject: "g-111", Verified: true},
		Purposes:  []string{"login", "contact"},
	}
	withRW(t, db, func(rw kv.ReadWriter) error { return s.putEmail(ctx, rw, "a@b.io", in) })

	withR(t, db, func(r kv.Reader) error {
		got, err := s.getEmail(ctx, r, "a@b.io")
		if err != nil {
			return err
		}
		if got.Synthetic != in.Synthetic {
			t.Errorf("Synthetic = %q, want %q", got.Synthetic, in.Synthetic)
		}
		if got.Provider == nil || *got.Provider != *in.Provider {
			t.Errorf("Provider = %+v, want %+v", got.Provider, in.Provider)
		}
		if !slices.Equal(got.Purposes, in.Purposes) {
			t.Errorf("Purposes = %v, want %v", got.Purposes, in.Purposes)
		}
		return nil
	})
}

func TestEmailRecNilProvider(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	withRW(t, db, func(rw kv.ReadWriter) error {
		return s.putEmail(ctx, rw, "x@y.io", &emailRec{Synthetic: "acct_2@id.hostcheck.invalid"})
	})
	withR(t, db, func(r kv.Reader) error {
		got, err := s.getEmail(ctx, r, "x@y.io")
		if err != nil {
			return err
		}
		if got.Provider != nil {
			t.Errorf("Provider = %+v, want nil", got.Provider)
		}
		if len(got.Purposes) != 0 {
			t.Errorf("Purposes = %v, want empty", got.Purposes)
		}
		return nil
	})
}

func TestIdentityRecRoundTrip(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	in := &identityRec{Emails: []Link{
		{Email: "a@b.io", Synthetic: "acct_1@id.hostcheck.invalid", Provider: &Provider{Name: "google"}, Purposes: []string{"login"}},
		{Email: "c@d.io", Synthetic: "acct_1@id.hostcheck.invalid"},
	}}
	withRW(t, db, func(rw kv.ReadWriter) error {
		return s.putIdentity(ctx, rw, "acct_1@id.hostcheck.invalid", in)
	})
	withR(t, db, func(r kv.Reader) error {
		got, err := s.getIdentity(ctx, r, "acct_1@id.hostcheck.invalid")
		if err != nil {
			return err
		}
		if len(got.Emails) != 2 {
			t.Fatalf("len(Emails) = %d, want 2", len(got.Emails))
		}
		if got.Emails[0].Email != "a@b.io" || got.Emails[0].Provider == nil || got.Emails[0].Provider.Name != "google" {
			t.Errorf("Emails[0] = %+v", got.Emails[0])
		}
		if got.Emails[1].Email != "c@d.io" || got.Emails[1].Provider != nil {
			t.Errorf("Emails[1] = %+v", got.Emails[1])
		}
		return nil
	})
}

func TestGetMissingIsNotExist(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	withR(t, db, func(r kv.Reader) error {
		if _, err := s.getEmail(ctx, r, "missing@x.io"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("getEmail miss = %v, want os.ErrNotExist", err)
		}
		if _, err := s.getIdentity(ctx, r, "missing@id.hostcheck.invalid"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("getIdentity miss = %v, want os.ErrNotExist", err)
		}
		return nil
	})
}

func TestDeleteRemovesRecord(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	withRW(t, db, func(rw kv.ReadWriter) error {
		return s.putEmail(ctx, rw, "gone@x.io", &emailRec{Synthetic: "acct_3@id.hostcheck.invalid"})
	})
	withRW(t, db, func(rw kv.ReadWriter) error { return s.delEmail(ctx, rw, "gone@x.io") })
	withR(t, db, func(r kv.Reader) error {
		if _, err := s.getEmail(ctx, r, "gone@x.io"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("after delEmail, get = %v, want os.ErrNotExist", err)
		}
		return nil
	})
}
