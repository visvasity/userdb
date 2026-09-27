// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultIdentityDomain is the identity domain used when WithIdentityDomain is
// not supplied. It uses the RFC 6761 ".invalid" special-use TLD, which is
// guaranteed never to resolve and never to be a real, deliverable domain
// (SPEC §9.4).
const DefaultIdentityDomain = "id.invalid"

// Sentinel errors, comparable with errors.Is. Missing records are reported as
// os.ErrNotExist by the read/write methods (added in later phases).
var (
	// ErrInvalidEmail indicates a human email failed validation (SPEC §4.2).
	ErrInvalidEmail = errors.New("userdb: invalid email")
	// ErrInvalidIdentity indicates a synthetic identity, identity domain, or
	// identity local part failed validation (SPEC §4.2, §4.5).
	ErrInvalidIdentity = errors.New("userdb: invalid synthetic identity")
	// ErrInvalidPurpose indicates a purpose tag failed validation (SPEC §4.4).
	ErrInvalidPurpose = errors.New("userdb: invalid purpose tag")
	// ErrConflict indicates the human email is already bound to a different
	// synthetic identity (SPEC §7.1).
	ErrConflict = errors.New("userdb: email already bound to a different identity")
)

// Provider is OPTIONAL provenance for a single Link: the identity provider that
// asserted the human email. All fields are optional; a zero Provider records
// only that some provider was involved (SPEC §3.4).
type Provider struct {
	Name     string // identity-provider id, e.g. "google", "github"
	Subject  string // provider's stable opaque subject (OIDC "sub")
	Verified bool   // provider asserted the email address is verified
}

// Link is one human email bound to one synthetic identity, with OPTIONAL
// provider data and OPTIONAL purpose tags. Email and Synthetic are the
// normalized (lower-cased) forms; Purposes is a normalized, deduplicated set
// (SPEC §3, §10).
type Link struct {
	Email     string
	Synthetic string
	Provider  *Provider // nil when no provenance was stored
	Purposes  []string  // app-defined labels this email is eligible for; may be empty
}

// Store is an exclusive key prefix under which one
// email-to-identity directory keeps all of its records. No other component may
// write under the same keyspace (SPEC §5).
type Store struct {
	keyspace       string
	identityDomain string
}

// Option configures a Store at construction.
type Option func(*Store)

// WithIdentityDomain sets the domain part appended by Identity. It defaults to
// DefaultIdentityDomain ("id.invalid"). The domain is validated by New; see
// SPEC §9.4 for guidance on choosing a non-resolving, non-deliverable domain.
func WithIdentityDomain(domain string) Option {
	return func(s *Store) { s.identityDomain = domain }
}

// New returns a directory that persists under the given keyspace (an exclusive
// key prefix; any trailing "/" is ignored). It fails if keyspace is empty, or if
// a supplied option is invalid (e.g. WithIdentityDomain given a malformed
// domain, which yields ErrInvalidIdentity).
func New(keyspace string, opts ...Option) (*Store, error) {
	if len(keyspace) == 0 {
		return nil, fmt.Errorf("userdb: keyspace cannot be empty")
	}
	s := &Store{
		keyspace:       strings.TrimSuffix(keyspace, "/"),
		identityDomain: DefaultIdentityDomain,
	}
	for _, o := range opts {
		o(s)
	}
	d, err := normDomain(s.identityDomain)
	if err != nil {
		return nil, fmt.Errorf("userdb: invalid identity domain %q: %w", s.identityDomain, err)
	}
	s.identityDomain = d
	return s, nil
}

// IdentityDomain returns the Store's configured identity domain.
func (s *Store) IdentityDomain() string { return s.identityDomain }

// Identity forms a synthetic identity "<local>@<domain>" from a caller-supplied
// local part (an opaque account id) and the Store's configured identity domain.
// It fails with ErrInvalidIdentity if local is malformed or the result is not a
// valid identity. Identity does not persist anything; the caller binds emails
// to the result with Link (SPEC §9.4, §10).
func (s *Store) Identity(local string) (string, error) {
	local = normalize(local)
	if !isValidLocalPart(local) {
		return "", ErrInvalidIdentity
	}
	id := local + "@" + s.identityDomain
	if !isValidAddress(id) {
		return "", ErrInvalidIdentity
	}
	return id, nil
}
