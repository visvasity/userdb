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

func mustMerge(t *testing.T, db kv.Database, s *Store, from, to string) {
	t.Helper()
	ctx := context.Background()
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Merge(ctx, rw, from, to) })
	checkConsistency(t, s, db)
}

func sortedEmails(ls []Link) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Email)
	}
	sort.Strings(out)
	return out
}

// ----- Phase 6: Merge -----

func TestMergeUnion(t *testing.T) {
	s, db := newTestStore(t)
	from, _ := s.Identity("acct_from")
	to, _ := s.Identity("acct_to")

	mustLink(t, db, s, "a@x.io", from, &Provider{Name: "github", Verified: true})
	mustLink(t, db, s, "b@y.io", from, nil)
	mustTag(t, db, s, "a@x.io", "login", "contact")
	mustLink(t, db, s, "c@z.io", to, &Provider{Name: "google"})

	mustMerge(t, db, s, from, to)

	// to now holds the union.
	if got := sortedEmails(emailsOf(t, db, s, to)); !slices.Equal(got, []string{"a@x.io", "b@y.io", "c@z.io"}) {
		t.Errorf("to emails = %v, want [a@x.io b@y.io c@z.io]", got)
	}
	// Moved emails resolve to `to`, with provider + purposes preserved.
	if id, p, err := resolve(t, db, s, "a@x.io"); err != nil || id != to || p == nil || !p.Verified {
		t.Errorf("a@x.io -> (%q, %+v, %v), want to + verified provider", id, p, err)
	}
	if got := purposesOf(t, db, s, to, "a@x.io"); !slices.Equal(got, []string{"login", "contact"}) {
		t.Errorf("a@x.io purposes after merge = %v, want [login contact]", got)
	}

	// from reverse record is gone.
	withR(t, db, func(r kv.Reader) error {
		if _, err := s.getIdentity(context.Background(), r, from); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("from identity record = %v, want os.ErrNotExist", err)
		}
		return nil
	})
}

func TestMergeIntoEmptyTarget(t *testing.T) {
	s, db := newTestStore(t)
	from, _ := s.Identity("acct_from")
	to, _ := s.Identity("acct_to") // never used

	mustLink(t, db, s, "a@x.io", from, nil)
	mustLink(t, db, s, "b@y.io", from, nil)

	mustMerge(t, db, s, from, to)

	if got := sortedEmails(emailsOf(t, db, s, to)); !slices.Equal(got, []string{"a@x.io", "b@y.io"}) {
		t.Errorf("to emails = %v, want [a@x.io b@y.io]", got)
	}
	if id, _, _ := resolve(t, db, s, "a@x.io"); id != to {
		t.Errorf("a@x.io resolves to %q, want %q", id, to)
	}
}

func TestMergeSelfNoop(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	mustLink(t, db, s, "a@x.io", id, nil)

	mustMerge(t, db, s, id, id)

	if got := sortedEmails(emailsOf(t, db, s, id)); !slices.Equal(got, []string{"a@x.io"}) {
		t.Errorf("after self-merge, emails = %v, want [a@x.io]", got)
	}
}

func TestMergeEmptySource(t *testing.T) {
	s, db := newTestStore(t)
	from, _ := s.Identity("never_used")
	to, _ := s.Identity("acct_to")
	mustLink(t, db, s, "c@z.io", to, nil)

	mustMerge(t, db, s, from, to) // from has nothing; no-op on to

	if got := sortedEmails(emailsOf(t, db, s, to)); !slices.Equal(got, []string{"c@z.io"}) {
		t.Errorf("to emails = %v, want [c@z.io]", got)
	}
}

func TestMergeInvalidIdentity(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	run := func(from, to string) error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return s.Merge(ctx, tx, from, to)
	}
	if err := run("bad identity", "a@id.hostcheck.invalid"); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("Merge bad from = %v, want ErrInvalidIdentity", err)
	}
	if err := run("a@id.hostcheck.invalid", "bad identity"); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("Merge bad to = %v, want ErrInvalidIdentity", err)
	}
}
