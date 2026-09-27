// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"
	"slices"
	"sort"
	"testing"

	"github.com/visvasity/kv"
)

func mustTag(t *testing.T, db kv.Database, s *Store, email string, purposes ...string) {
	t.Helper()
	ctx := context.Background()
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Tag(ctx, rw, email, purposes...) })
	checkConsistency(t, s, db)
}

func mustUntag(t *testing.T, db kv.Database, s *Store, email string, purposes ...string) {
	t.Helper()
	ctx := context.Background()
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Untag(ctx, rw, email, purposes...) })
	checkConsistency(t, s, db)
}

func purposesOf(t *testing.T, db kv.Database, s *Store, id, email string) []string {
	t.Helper()
	for _, l := range emailsOf(t, db, s, id) {
		if l.Email == email {
			return l.Purposes
		}
	}
	t.Fatalf("email %q not found under identity %q", email, id)
	return nil
}

func emailsForSorted(t *testing.T, db kv.Database, s *Store, id, purpose string) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	withR(t, db, func(r kv.Reader) error {
		ls, err := s.EmailsFor(ctx, r, id, purpose)
		if err != nil {
			return err
		}
		for _, l := range ls {
			out = append(out, l.Email)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ----- Phase 5: purpose tags -----

func TestTagAndUntag(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	mustLink(t, db, s, "a@x.io", id, nil)

	mustTag(t, db, s, "A@X.io", "Login", "contact") // case-insensitive email + tags
	if got := purposesOf(t, db, s, id, "a@x.io"); !slices.Equal(got, []string{"login", "contact"}) {
		t.Errorf("purposes = %v, want [login contact]", got)
	}

	// Idempotent add.
	mustTag(t, db, s, "a@x.io", "login")
	if got := purposesOf(t, db, s, id, "a@x.io"); !slices.Equal(got, []string{"login", "contact"}) {
		t.Errorf("purposes after dup add = %v, want [login contact]", got)
	}

	// Remove one; removing an absent tag is a no-op.
	mustUntag(t, db, s, "a@x.io", "login", "billing")
	if got := purposesOf(t, db, s, id, "a@x.io"); !slices.Equal(got, []string{"contact"}) {
		t.Errorf("purposes after untag = %v, want [contact]", got)
	}
}

func TestTagUnboundIsNotExist(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	run := func(fn func(rw kv.ReadWriter) error) error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return fn(tx)
	}
	if err := run(func(rw kv.ReadWriter) error { return s.Tag(ctx, rw, "nobody@x.io", "login") }); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Tag unbound = %v, want os.ErrNotExist", err)
	}
	if err := run(func(rw kv.ReadWriter) error { return s.Untag(ctx, rw, "nobody@x.io", "login") }); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Untag unbound = %v, want os.ErrNotExist", err)
	}
}

func TestTagInvalidPurposeNoChange(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	mustLink(t, db, s, "a@x.io", id, nil)
	mustTag(t, db, s, "a@x.io", "login")

	ctx := context.Background()
	err := func() error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return s.Tag(ctx, tx, "a@x.io", "ok", "bad tag")
	}()
	if !errors.Is(err, ErrInvalidPurpose) {
		t.Fatalf("Tag invalid purpose = %v, want ErrInvalidPurpose", err)
	}
	// Nothing changed: still just [login].
	if got := purposesOf(t, db, s, id, "a@x.io"); !slices.Equal(got, []string{"login"}) {
		t.Errorf("purposes after failed tag = %v, want [login]", got)
	}
}

func TestEmailsForManyToMany(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "a@x.io", id, nil)
	mustLink(t, db, s, "b@y.io", id, nil)
	mustLink(t, db, s, "c@z.io", id, nil)

	// Two emails share "contact"; one is login-only.
	mustTag(t, db, s, "a@x.io", "contact", "login")
	mustTag(t, db, s, "b@y.io", "contact")
	mustTag(t, db, s, "c@z.io", "login")

	if got := emailsForSorted(t, db, s, id, "contact"); !slices.Equal(got, []string{"a@x.io", "b@y.io"}) {
		t.Errorf("EmailsFor(contact) = %v, want [a@x.io b@y.io]", got)
	}
	if got := emailsForSorted(t, db, s, id, "login"); !slices.Equal(got, []string{"a@x.io", "c@z.io"}) {
		t.Errorf("EmailsFor(login) = %v, want [a@x.io c@z.io]", got)
	}
	if got := emailsForSorted(t, db, s, id, "billing"); len(got) != 0 {
		t.Errorf("EmailsFor(billing) = %v, want empty", got)
	}
	// Case-insensitive purpose.
	if got := emailsForSorted(t, db, s, id, "CONTACT"); !slices.Equal(got, []string{"a@x.io", "b@y.io"}) {
		t.Errorf("EmailsFor(CONTACT) = %v, want [a@x.io b@y.io]", got)
	}
}

func TestPurposesSurviveReLink(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "a@x.io", id, &Provider{Name: "google"})
	mustTag(t, db, s, "a@x.io", "login", "contact")

	// Idempotent re-Link (same identity, new provider) must preserve purposes.
	mustLink(t, db, s, "a@x.io", id, &Provider{Name: "google", Verified: true})

	if got := purposesOf(t, db, s, id, "a@x.io"); !slices.Equal(got, []string{"login", "contact"}) {
		t.Errorf("purposes after re-link = %v, want [login contact]", got)
	}
	_, p, _ := resolve(t, db, s, "a@x.io")
	if p == nil || !p.Verified {
		t.Errorf("provider after re-link = %+v, want verified", p)
	}
}

func TestEmailsForEmptyIdentity(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("unused")
	if got := emailsForSorted(t, db, s, id, "contact"); len(got) != 0 {
		t.Errorf("EmailsFor(empty identity) = %v, want empty", got)
	}
}
