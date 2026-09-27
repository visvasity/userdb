// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/visvasity/kv"
	"github.com/visvasity/kv/kvutil"
)

// checkConsistency asserts the forward↔reverse bijection (SPEC §3.1): every
// forward record has a matching reverse entry and vice versa. Reused by later
// phases.
func checkConsistency(t *testing.T, s *Store, db kv.Database) {
	t.Helper()
	ctx := context.Background()
	withR(t, db, func(r kv.Reader) error {
		// Forward -> reverse: each e/<email> maps to an identity whose reverse
		// record contains a Link for that email with the same identity.
		ebeg, eend := kvutil.PrefixRange(s.keyspace + emailPrefix)
		var err error
		for key, rec := range kvutil.AscendGob[emailRec](ctx, r, ebeg, eend, &err) {
			email := strings.TrimPrefix(key, s.keyspace+emailPrefix)
			id := rec.Synthetic
			irec, ierr := s.getIdentity(ctx, r, id)
			if ierr != nil {
				t.Errorf("forward %q -> identity %q has no reverse record: %v", email, id, ierr)
				continue
			}
			found := false
			for _, l := range irec.Emails {
				if l.Email == email {
					found = true
					if l.Synthetic != id {
						t.Errorf("reverse Link for %q has Synthetic %q, want %q", email, l.Synthetic, id)
					}
				}
			}
			if !found {
				t.Errorf("forward %q -> %q not present in reverse record", email, id)
			}
		}
		if err != nil {
			t.Fatalf("scan forward: %v", err)
		}

		// Reverse -> forward: each Link in every s/<identity> record resolves
		// back to a forward record with the same identity.
		sbeg, send := kvutil.PrefixRange(s.keyspace + identityPrefix)
		for key, rec := range kvutil.AscendGob[identityRec](ctx, r, sbeg, send, &err) {
			id := strings.TrimPrefix(key, s.keyspace+identityPrefix)
			if len(rec.Emails) == 0 {
				t.Errorf("reverse record %q is empty (should have been deleted)", id)
			}
			for _, l := range rec.Emails {
				erec, eerr := s.getEmail(ctx, r, l.Email)
				if eerr != nil {
					t.Errorf("reverse %q lists %q with no forward record: %v", id, l.Email, eerr)
					continue
				}
				if erec.Synthetic != id {
					t.Errorf("reverse %q lists %q but forward says %q", id, l.Email, erec.Synthetic)
				}
			}
		}
		if err != nil {
			t.Fatalf("scan reverse: %v", err)
		}
		return nil
	})
}

func mustLink(t *testing.T, db kv.Database, s *Store, email, id string, p *Provider) {
	t.Helper()
	ctx := context.Background()
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Link(ctx, rw, email, id, p) })
	checkConsistency(t, s, db)
}

func resolve(t *testing.T, db kv.Database, s *Store, email string) (string, *Provider, error) {
	t.Helper()
	ctx := context.Background()
	var id string
	var p *Provider
	var err error
	withR(t, db, func(r kv.Reader) error {
		id, p, err = s.Resolve(ctx, r, email)
		return nil
	})
	return id, p, err
}

// ----- Phase 3: forward core -----

func TestLinkAndResolve(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "Alice@Example.com", id, &Provider{Name: "google", Verified: true})

	// Case-insensitive resolve.
	for _, q := range []string{"alice@example.com", "ALICE@EXAMPLE.COM", "  Alice@Example.com "} {
		got, p, err := resolve(t, db, s, q)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", q, err)
		}
		if got != id {
			t.Errorf("Resolve(%q) = %q, want %q", q, got, id)
		}
		if p == nil || p.Name != "google" || !p.Verified {
			t.Errorf("Resolve(%q) provider = %+v", q, p)
		}
	}
}

func TestManyEmailsToOneIdentity(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "a@x.io", id, nil)
	mustLink(t, db, s, "b@y.io", id, &Provider{Name: "github"})
	mustLink(t, db, s, "c@z.io", id, nil)

	for _, e := range []string{"a@x.io", "b@y.io", "c@z.io"} {
		got, _, err := resolve(t, db, s, e)
		if err != nil || got != id {
			t.Errorf("Resolve(%q) = (%q, %v), want %q", e, got, err, id)
		}
	}
}

func TestLinkConflict(t *testing.T) {
	s, db := newTestStore(t)
	id1, _ := s.Identity("acct_1")
	id2, _ := s.Identity("acct_2")
	ctx := context.Background()

	mustLink(t, db, s, "a@x.io", id1, nil)

	// Re-binding to a different identity must fail and change nothing.
	err := func() error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return s.Link(ctx, tx, "a@x.io", id2, nil)
	}()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Link to different identity = %v, want ErrConflict", err)
	}
	if got, _, _ := resolve(t, db, s, "a@x.io"); got != id1 {
		t.Errorf("after conflict, Resolve = %q, want unchanged %q", got, id1)
	}
	checkConsistency(t, s, db)
}

func TestLinkIdempotentUpdatesProvider(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")

	mustLink(t, db, s, "a@x.io", id, &Provider{Name: "google", Verified: false})
	// Re-link same identity with new provider data: last-writer-wins.
	mustLink(t, db, s, "a@x.io", id, &Provider{Name: "google", Subject: "g-9", Verified: true})

	_, p, err := resolve(t, db, s, "a@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.Subject != "g-9" || !p.Verified {
		t.Errorf("provider after re-link = %+v, want {google g-9 true}", p)
	}
	// Still exactly one reverse entry.
	withR(t, db, func(r kv.Reader) error {
		rec, err := s.getIdentity(context.Background(), r, id)
		if err != nil {
			return err
		}
		if len(rec.Emails) != 1 {
			t.Errorf("reverse entries = %d, want 1", len(rec.Emails))
		}
		return nil
	})
}

func TestUnlinkLeavesSiblings(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	ctx := context.Background()

	mustLink(t, db, s, "a@x.io", id, nil)
	mustLink(t, db, s, "b@y.io", id, nil)

	withRW(t, db, func(rw kv.ReadWriter) error { return s.Unlink(ctx, rw, "A@X.io") }) // case-insensitive
	checkConsistency(t, s, db)

	if _, _, err := resolve(t, db, s, "a@x.io"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Resolve unlinked = %v, want os.ErrNotExist", err)
	}
	if got, _, err := resolve(t, db, s, "b@y.io"); err != nil || got != id {
		t.Errorf("sibling Resolve = (%q, %v), want %q", got, err, id)
	}
}

func TestUnlinkLastDeletesIdentityRecord(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	ctx := context.Background()

	mustLink(t, db, s, "only@x.io", id, nil)
	withRW(t, db, func(rw kv.ReadWriter) error { return s.Unlink(ctx, rw, "only@x.io") })
	checkConsistency(t, s, db)

	withR(t, db, func(r kv.Reader) error {
		if _, err := s.getIdentity(ctx, r, id); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("emptied identity record = %v, want os.ErrNotExist", err)
		}
		return nil
	})
}

func TestUnlinkUnboundIsNotExist(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	err := func() error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return s.Unlink(ctx, tx, "nobody@x.io")
	}()
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Unlink unbound = %v, want os.ErrNotExist", err)
	}
}

func TestForwardCoreInvalidInputs(t *testing.T) {
	s, db := newTestStore(t)
	id, _ := s.Identity("acct_1")
	ctx := context.Background()

	run := func(fn func(rw kv.ReadWriter) error) error {
		tx, _ := db.NewTransaction(ctx)
		defer tx.Rollback(ctx)
		return fn(tx)
	}
	if err := run(func(rw kv.ReadWriter) error { return s.Link(ctx, rw, "bad email", id, nil) }); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("Link bad email = %v, want ErrInvalidEmail", err)
	}
	if err := run(func(rw kv.ReadWriter) error { return s.Link(ctx, rw, "a@x.io", "not-an-identity", nil) }); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("Link bad identity = %v, want ErrInvalidIdentity", err)
	}
	if _, _, err := resolve(t, db, s, "bad email"); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("Resolve bad email = %v, want ErrInvalidEmail", err)
	}
}
