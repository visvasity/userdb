// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"context"

	"github.com/visvasity/kv"
	"github.com/visvasity/kv/kvutil"
)

// On-disk key classes under the Store keyspace (SPEC §5). The email and
// identity strings are already normalized and free of '/', so raw keys are
// safe, debuggable, and prefix-scannable.
const (
	emailPrefix    = "/e/" // forward: <keyspace>/e/<email> -> emailRec
	identityPrefix = "/s/" // reverse: <keyspace>/s/<identity> -> identityRec
)

// emailRec is the forward record: the source of truth for a single email's
// identity binding, provider data, and purpose tags (SPEC §5).
type emailRec struct {
	Synthetic string
	Provider  *Provider
	Purposes  []string
}

// identityRec is the reverse record: every email bound to one identity,
// denormalized with provider data and purpose tags so Emails/EmailsFor are a
// single read (SPEC §5).
type identityRec struct {
	Emails []Link
}

func (s *Store) emailKey(email string) string { return s.keyspace + emailPrefix + email }
func (s *Store) identityKey(id string) string { return s.keyspace + identityPrefix + id }

// getEmail reads the forward record for a normalized email. A missing record is
// reported as os.ErrNotExist (wrapped).
func (s *Store) getEmail(ctx context.Context, r kv.Reader, email string) (*emailRec, error) {
	var rec emailRec
	if err := kvutil.GetGob(ctx, r, s.emailKey(email), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// putEmail creates or replaces the forward record for a normalized email.
func (s *Store) putEmail(ctx context.Context, rw kv.ReadWriter, email string, rec *emailRec) error {
	return kvutil.SetGob(ctx, rw, s.emailKey(email), rec)
}

// delEmail removes the forward record for a normalized email.
func (s *Store) delEmail(ctx context.Context, rw kv.ReadWriter, email string) error {
	return rw.Delete(ctx, s.emailKey(email))
}

// getIdentity reads the reverse record for a normalized identity. A missing
// record is reported as os.ErrNotExist (wrapped).
func (s *Store) getIdentity(ctx context.Context, r kv.Reader, id string) (*identityRec, error) {
	var rec identityRec
	if err := kvutil.GetGob(ctx, r, s.identityKey(id), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// putIdentity creates or replaces the reverse record for a normalized identity.
func (s *Store) putIdentity(ctx context.Context, rw kv.ReadWriter, id string, rec *identityRec) error {
	return kvutil.SetGob(ctx, rw, s.identityKey(id), rec)
}

// delIdentity removes the reverse record for a normalized identity.
func (s *Store) delIdentity(ctx context.Context, rw kv.ReadWriter, id string) error {
	return rw.Delete(ctx, s.identityKey(id))
}
