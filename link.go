// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"

	"github.com/visvasity/kv"
)

// Link binds email to the synthetic identity. Many emails MAY map to one
// identity, but a single email maps to at most one identity (SPEC §3.1, §7.1).
//
//   - If email is already bound to a DIFFERENT identity, Link fails with
//     ErrConflict and changes nothing.
//   - If email is unbound, Link creates the forward record and adds it to the
//     identity's reverse record (creating that record if absent).
//   - If email is already bound to the SAME identity, Link is idempotent with
//     respect to the binding: it updates the stored provider data
//     (last-writer-wins) while preserving the email's existing purpose tags.
//
// provider MAY be nil. Both the forward and reverse records are updated within
// rw so the two directions never drift.
func (s *Store) Link(ctx context.Context, rw kv.ReadWriter, email, synthetic string, provider *Provider) error {
	em, err := normEmail(email)
	if err != nil {
		return err
	}
	id, err := normIdentity(synthetic)
	if err != nil {
		return err
	}

	cur, err := s.getEmail(ctx, rw, em)
	switch {
	case err == nil:
		if cur.Synthetic != id {
			return ErrConflict
		}
		// Same identity: update provider, preserve purposes.
		cur.Provider = cloneProvider(provider)
		if err := s.putEmail(ctx, rw, em, cur); err != nil {
			return err
		}
		return s.upsertReverse(ctx, rw, id, Link{
			Email:     em,
			Synthetic: id,
			Provider:  cloneProvider(provider),
			Purposes:  cur.Purposes,
		})

	case errors.Is(err, os.ErrNotExist):
		if err := s.putEmail(ctx, rw, em, &emailRec{Synthetic: id, Provider: cloneProvider(provider)}); err != nil {
			return err
		}
		return s.upsertReverse(ctx, rw, id, Link{
			Email:     em,
			Synthetic: id,
			Provider:  cloneProvider(provider),
		})

	default:
		return err
	}
}

// Unlink removes the binding for email. Other emails bound to the same identity
// are unaffected. It returns os.ErrNotExist if email is not bound (SPEC §7.2).
func (s *Store) Unlink(ctx context.Context, rw kv.ReadWriter, email string) error {
	em, err := normEmail(email)
	if err != nil {
		return err
	}
	cur, err := s.getEmail(ctx, rw, em)
	if err != nil {
		return err // os.ErrNotExist propagates
	}
	id := cur.Synthetic

	rec, err := s.getIdentity(ctx, rw, id)
	switch {
	case err == nil:
		removeLink(rec, em)
		if len(rec.Emails) == 0 {
			if err := s.delIdentity(ctx, rw, id); err != nil {
				return err
			}
		} else if err := s.putIdentity(ctx, rw, id, rec); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return s.delEmail(ctx, rw, em)
}

// Resolve returns the synthetic identity and OPTIONAL provider data bound to
// email (the many-to-one, forward direction). It returns os.ErrNotExist if
// email is not bound (SPEC §7.3).
func (s *Store) Resolve(ctx context.Context, r kv.Reader, email string) (synthetic string, provider *Provider, err error) {
	em, err := normEmail(email)
	if err != nil {
		return "", nil, err
	}
	rec, err := s.getEmail(ctx, r, em)
	if err != nil {
		return "", nil, err
	}
	return rec.Synthetic, rec.Provider, nil
}

// upsertReverse adds or replaces l in the reverse record of id, creating the
// record if absent.
func (s *Store) upsertReverse(ctx context.Context, rw kv.ReadWriter, id string, l Link) error {
	rec, err := s.getIdentity(ctx, rw, id)
	if errors.Is(err, os.ErrNotExist) {
		rec = &identityRec{}
	} else if err != nil {
		return err
	}
	upsertLink(rec, l)
	return s.putIdentity(ctx, rw, id, rec)
}

// upsertLink replaces the entry for l.Email in rec, or appends it if absent.
func upsertLink(rec *identityRec, l Link) {
	for i := range rec.Emails {
		if rec.Emails[i].Email == l.Email {
			rec.Emails[i] = l
			return
		}
	}
	rec.Emails = append(rec.Emails, l)
}

// removeLink removes the entry for email from rec, reporting whether it was
// present.
func removeLink(rec *identityRec, email string) bool {
	for i := range rec.Emails {
		if rec.Emails[i].Email == email {
			rec.Emails = append(rec.Emails[:i], rec.Emails[i+1:]...)
			return true
		}
	}
	return false
}

// cloneProvider returns a shallow copy of p, or nil if p is nil, so stored
// records never alias the caller's Provider.
func cloneProvider(p *Provider) *Provider {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
