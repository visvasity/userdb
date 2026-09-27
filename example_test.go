// Copyright (c) 2026 Visvasity LLC

package userdb_test

import (
	"context"
	"fmt"

	"github.com/visvasity/kv"
	"github.com/visvasity/kvmemdb"
	"github.com/visvasity/userdb"
)

// Example shows binding two provider emails to one synthetic identity, resolving
// a human email to it (case-insensitively), and enumerating the identity's
// contact addresses.
func Example() {
	ctx := context.Background()
	s, err := userdb.New("/userdb", userdb.WithIdentityDomain("id.hostcheck.invalid"))
	if err != nil {
		panic(err)
	}
	db := kv.DatabaseFrom(kvmemdb.New())

	// The caller mints the account id; userdb appends the identity domain.
	id, err := s.Identity("acct_7f3")
	if err != nil {
		panic(err)
	}

	// Bind two emails (Google + GitHub) to the one identity, and tag one as a
	// contact address — all in a single transaction.
	tx, err := db.NewTransaction(ctx)
	if err != nil {
		panic(err)
	}
	if err := s.Link(ctx, tx, "Alice@Example.com", id, &userdb.Provider{Name: "google", Verified: true}); err != nil {
		panic(err)
	}
	if err := s.Link(ctx, tx, "alice@work.io", id, &userdb.Provider{Name: "github", Verified: true}); err != nil {
		panic(err)
	}
	if err := s.Tag(ctx, tx, "alice@work.io", "contact"); err != nil {
		panic(err)
	}
	if err := tx.Commit(ctx); err != nil {
		panic(err)
	}

	snap, err := db.NewSnapshot(ctx)
	if err != nil {
		panic(err)
	}
	defer snap.Discard(ctx)

	got, _, _ := s.Resolve(ctx, snap, "ALICE@EXAMPLE.COM")
	fmt.Println("identity:", got)

	contacts, _ := s.EmailsFor(ctx, snap, id, "contact")
	fmt.Println("contacts:", len(contacts), contacts[0].Email)

	// Output:
	// identity: acct_7f3@id.hostcheck.invalid
	// contacts: 1 alice@work.io
}
