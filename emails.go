// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"
	"errors"
	"os"

	"github.com/visvasity/kv"
)

// Emails returns every human email bound to the synthetic identity, each with
// its OPTIONAL provider data and purpose tags (the one-to-many, reverse
// direction). An identity with no bound emails yields an empty slice and a nil
// error (SPEC §7.4).
func (s *Store) Emails(ctx context.Context, r kv.Reader, synthetic string) ([]Link, error) {
	id, err := normIdentity(synthetic)
	if err != nil {
		return nil, err
	}
	rec, err := s.getIdentity(ctx, r, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// rec.Emails is freshly decoded from gob on each read, so it is already an
	// independent copy the caller may retain and mutate.
	return rec.Emails, nil
}
