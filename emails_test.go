// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/visvasity/kv"
)

// emailsOf returns the reverse enumeration for id, failing the test on error.
func emailsOf(t *testing.T, db kv.Database, s *Store, id string) []Link {
	t.Helper()
	ctx := context.Background()
	var out []Link
	withR(t, db, func(r kv.Reader) error {
		var err error
		out, err = s.Emails(ctx, r, id)
		return err
	})
	return out
}

// ----- Phase 4: reverse enumeration -----

func TestEmailsEnumerates(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "a@x.io", id, &Provider{Name: "google", Verified: true})
	mustLink(t, db, s, "b@y.io", id, nil)
	mustLink(t, db, s, "c@z.io", id, &Provider{Name: "github"})

	got := emailsOf(t, db, s, id)
	if len(got) != 3 {
		t.Fatalf("Emails len = %d, want 3", len(got))
	}

	// Every returned Link resolves back to the same identity (no drift), and
	// provider data is preserved.
	byEmail := map[string]Link{}
	for _, l := range got {
		if l.Synthetic != id {
			t.Errorf("Link %q Synthetic = %q, want %q", l.Email, l.Synthetic, id)
		}
		rid, _, err := resolve(t, db, s, l.Email)
		if err != nil || rid != id {
			t.Errorf("returned email %q resolves to (%q, %v), want %q", l.Email, rid, err, id)
		}
		byEmail[l.Email] = l
	}
	if p := byEmail["a@x.io"].Provider; p == nil || p.Name != "google" || !p.Verified {
		t.Errorf("a@x.io provider = %+v", p)
	}
	if byEmail["b@y.io"].Provider != nil {
		t.Errorf("b@y.io provider = %+v, want nil", byEmail["b@y.io"].Provider)
	}

	emails := []string{got[0].Email, got[1].Email, got[2].Email}
	sort.Strings(emails)
	want := []string{"a@x.io", "b@y.io", "c@z.io"}
	for i := range want {
		if emails[i] != want[i] {
			t.Errorf("emails = %v, want set %v", emails, want)
			break
		}
	}
}

func TestEmailsEmptyIdentity(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("never_used")

	got := emailsOf(t, db, s, id)
	if got != nil && len(got) != 0 {
		t.Errorf("Emails of unused identity = %v, want empty", got)
	}
}

func TestEmailsCaseInsensitive(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	mustLink(t, db, s, "a@x.io", id, nil)

	if got := emailsOf(t, db, s, "ACCT_1@ID.Hostcheck.Invalid"); len(got) != 1 || got[0].Email != "a@x.io" {
		t.Errorf("case-insensitive Emails = %v, want [a@x.io]", got)
	}
}

func TestEmailsReflectsUnlink(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	ctx := context.Background()

	mustLink(t, db, s, "a@x.io", id, nil)
	mustLink(t, db, s, "b@y.io", id, nil)
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Unlink(ctx, rw, "a@x.io") })

	got := emailsOf(t, db, s, id)
	if len(got) != 1 || got[0].Email != "b@y.io" {
		t.Errorf("after unlink, Emails = %v, want [b@y.io]", got)
	}
}

func TestEmailsInvalidIdentity(t *testing.T) {
	s, db := newTestStore(t)
	if _, err := func() ([]Link, error) {
		ctx := context.Background()
		snap, _ := db.NewSnapshot(ctx)
		defer snap.Discard(ctx)
		return s.Emails(ctx, snap, "not-an-identity")
	}(); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("Emails bad identity = %v, want ErrInvalidIdentity", err)
	}
}
