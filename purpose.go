// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"

	"github.com/visvasity/kv"
)

// Tag adds one or more purpose tags to a bound email. It returns os.ErrNotExist
// if email is not bound and ErrInvalidPurpose if any tag is invalid (in which
// case nothing changes). Adding a tag already present is a no-op. Both the
// forward and reverse records are updated within rw (SPEC §7.6).
func (s *Store) Tag(ctx context.Context, rw kv.ReadWriter, email string, purposes ...string) error {
	em, err := normEmail(email)
	if err != nil {
		return err
	}
	add, err := normPurposeSet(purposes)
	if err != nil {
		return err
	}
	rec, err := s.getEmail(ctx, rw, em)
	if err != nil {
		return err // os.ErrNotExist propagates
	}
	if len(add) == 0 {
		return nil
	}
	rec.Purposes = addPurposes(rec.Purposes, add)
	return s.writeEmailBoth(ctx, rw, em, rec)
}

// Untag removes one or more purpose tags from a bound email. It returns
// os.ErrNotExist if email is not bound. Removing an absent tag is a no-op
// (SPEC §7.7).
func (s *Store) Untag(ctx context.Context, rw kv.ReadWriter, email string, purposes ...string) error {
	em, err := normEmail(email)
	if err != nil {
		return err
	}
	rm, err := normPurposeSet(purposes)
	if err != nil {
		return err
	}
	rec, err := s.getEmail(ctx, rw, em)
	if err != nil {
		return err // os.ErrNotExist propagates
	}
	if len(rm) == 0 {
		return nil
	}
	rec.Purposes = removePurposes(rec.Purposes, rm)
	return s.writeEmailBoth(ctx, rw, em, rec)
}

// EmailsFor returns the subset of the synthetic identity's bound emails whose
// tag set contains purpose. It never selects a single "primary"; if several
// match, all are returned. No match yields an empty slice and a nil error
// (SPEC §7.8).
func (s *Store) EmailsFor(ctx context.Context, r kv.Reader, synthetic, purpose string) ([]Link, error) {
	p, err := normPurpose(purpose)
	if err != nil {
		return nil, err
	}
	all, err := s.Emails(ctx, r, synthetic)
	if err != nil {
		return nil, err
	}
	var out []Link
	for _, l := range all {
		if hasPurpose(l.Purposes, p) {
			out = append(out, l)
		}
	}
	return out, nil
}

// writeEmailBoth persists the forward record and mirrors its identity binding,
// provider, and purposes into the reverse record.
func (s *Store) writeEmailBoth(ctx context.Context, rw kv.ReadWriter, em string, rec *emailRec) error {
	if err := s.putEmail(ctx, rw, em, rec); err != nil {
		return err
	}
	return s.upsertReverse(ctx, rw, rec.Synthetic, Link{
		Email:     em,
		Synthetic: rec.Synthetic,
		Provider:  cloneProvider(rec.Provider),
		Purposes:  rec.Purposes,
	})
}

// addPurposes returns existing with each of add appended if not already
// present, preserving order.
func addPurposes(existing, add []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(add))
	out := make([]string, 0, len(existing)+len(add))
	for _, p := range existing {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for _, p := range add {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// removePurposes returns existing with every element of remove dropped,
// preserving order; an emptied result is returned as nil.
func removePurposes(existing, remove []string) []string {
	rm := make(map[string]struct{}, len(remove))
	for _, p := range remove {
		rm[p] = struct{}{}
	}
	out := make([]string, 0, len(existing))
	for _, p := range existing {
		if _, ok := rm[p]; !ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hasPurpose reports whether ps contains p.
func hasPurpose(ps []string, p string) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}
