// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/visvasity/kv"
)

// TestRandomizedInvariants applies a long, deterministic mix of every mutating
// operation and asserts the global invariants after each step via
// checkConsistency (forward↔reverse bijection, no email bound to two
// identities, and no provider/purpose drift). Operation errors (ErrConflict,
// os.ErrNotExist, ...) are legitimate outcomes and are ignored.
func TestRandomizedInvariants(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1))

	emails := []string{"e0@x.io", "e1@x.io", "e2@x.io", "e3@x.io", "e4@x.io"}
	ids := make([]string, 4)
	for i := range ids {
		ids[i], _ = s.Identity(fmt.Sprintf("acct_%d", i))
	}
	purposes := []string{"login", "contact", "billing"}

	pick := func(ss []string) string { return ss[rng.Intn(len(ss))] }

	const steps = 600
	for i := 0; i < steps; i++ {
		op := rng.Intn(5)
		withRW(t, db, func(rw kv.ReadWriter) error {
			var err error
			switch op {
			case 0:
				var p *Provider
				if rng.Intn(2) == 0 {
					p = &Provider{Name: pick([]string{"google", "github"}), Verified: rng.Intn(2) == 0}
				}
				err = s.Link(ctx, rw, pick(emails), pick(ids), p)
			case 1:
				err = s.Unlink(ctx, rw, pick(emails))
			case 2:
				err = s.Tag(ctx, rw, pick(emails), pick(purposes))
			case 3:
				err = s.Untag(ctx, rw, pick(emails), pick(purposes))
			case 4:
				err = s.Merge(ctx, rw, pick(ids), pick(ids))
			}
			// Only genuine, expected outcomes are tolerated.
			if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("step %d op %d: unexpected error: %v", i, op, err)
			}
			return nil
		})
		checkConsistency(t, s, db)
	}
}

// TestTransactionComposition proves a userdb mutation composes atomically with
// the caller's own writes in a single transaction: on commit both persist, on
// rollback neither does.
func TestTransactionComposition(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	id, _ := s.Identity("acct_1")

	// Commit: Link + a caller key land together.
	tx, err := db.NewTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Link(ctx, tx, "a@x.io", id, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Set(ctx, "/app/account/a", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _, err := resolve(t, db, s, "a@x.io"); err != nil || got != id {
		t.Errorf("after commit, resolve = (%q, %v), want %q", got, err, id)
	}
	withR(t, db, func(r kv.Reader) error {
		if _, err := r.Get(ctx, "/app/account/a"); err != nil {
			t.Errorf("caller key missing after commit: %v", err)
		}
		return nil
	})

	// Rollback: Link + a caller key both vanish.
	tx2, err := db.NewTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Link(ctx, tx2, "b@y.io", id, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Set(ctx, "/app/account/b", strings.NewReader("world")); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolve(t, db, s, "b@y.io"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after rollback, resolve = %v, want os.ErrNotExist", err)
	}
	withR(t, db, func(r kv.Reader) error {
		if _, err := r.Get(ctx, "/app/account/b"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("caller key present after rollback: %v", err)
		}
		return nil
	})
	checkConsistency(t, s, db)
}
