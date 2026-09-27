// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"

	"github.com/visvasity/kv"
)

// Merge re-binds every email of the from identity onto the to identity,
// preserving provider data and purpose tags, and deletes the from identity's
// reverse record. It supports account merges: two identities discovered to name
// the same subject. A from == to call is a no-op, as is merging an identity
// that has no bound emails (SPEC §7.5).
//
// Because each email had exactly one binding (to from), moving it to to cannot
// conflict; to accumulates the union of both identities' emails. Both the
// forward and reverse records are updated within rw so the two directions never
// drift.
func (s *Store) Merge(ctx context.Context, rw kv.ReadWriter, from, to string) error {
	fromID, err := normIdentity(from)
	if err != nil {
		return err
	}
	toID, err := normIdentity(to)
	if err != nil {
		return err
	}
	if fromID == toID {
		return nil
	}

	frec, err := s.getIdentity(ctx, rw, fromID)
	if errors.Is(err, os.ErrNotExist) {
		return nil // nothing bound to from
	}
	if err != nil {
		return err
	}

	trec, err := s.getIdentity(ctx, rw, toID)
	if errors.Is(err, os.ErrNotExist) {
		trec = &identityRec{}
	} else if err != nil {
		return err
	}

	for _, l := range frec.Emails {
		// The forward record is the source of truth for provider and purposes.
		erec, err := s.getEmail(ctx, rw, l.Email)
		if err != nil {
			return err
		}
		erec.Synthetic = toID
		if err := s.putEmail(ctx, rw, l.Email, erec); err != nil {
			return err
		}
		upsertLink(trec, Link{
			Email:     l.Email,
			Synthetic: toID,
			Provider:  cloneProvider(erec.Provider),
			Purposes:  erec.Purposes,
		})
	}

	if err := s.putIdentity(ctx, rw, toID, trec); err != nil {
		return err
	}
	return s.delIdentity(ctx, rw, fromID)
}
