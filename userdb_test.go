// Copyright (c) 2026 Visvasity LLC

package userdb

import (
	"errors"
	"slices"
	"testing"
)

// ----- Phase 1: normalization & validation (pure) -----

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alice@Example.COM", "alice@example.com"},
		{"  spaced@x.io\t", "spaced@x.io"},
		{"\n\r Mixed@Case.Test \v", "mixed@case.test"},
		{"already@lower.io", "already@lower.io"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
		// Idempotency.
		if got := normalize(normalize(c.in)); got != c.want {
			t.Errorf("normalize not idempotent for %q", c.in)
		}
	}
}

func TestNormEmail(t *testing.T) {
	good := []struct{ in, want string }{
		{"User@Host.io", "user@host.io"},
		{"  a@b.co  ", "a@b.co"},
		{"tag+plus@x.io", "tag+plus@x.io"},
		{"dotted.name@sub.domain.io", "dotted.name@sub.domain.io"},
	}
	for _, c := range good {
		got, err := normEmail(c.in)
		if err != nil {
			t.Errorf("normEmail(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	bad := []string{
		"",               // empty
		"no-at-sign",     // missing '@'
		"@nolocal.io",    // empty local
		"nodomain@",      // empty domain
		"two@ats@x.io",   // more than one '@'
		"a b@x.io",       // space
		"a/b@x.io",       // '/'
		"a#b@x.io",       // '#'
		"a\x00b@x.io",    // NUL
		"ctl\x07@x.io",   // control byte
		"trail@x.io/sub", // '/' in domain
	}
	for _, s := range bad {
		if _, err := normEmail(s); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("normEmail(%q) error = %v, want ErrInvalidEmail", s, err)
		}
	}
}

func TestNormIdentity(t *testing.T) {
	if got, err := normIdentity("Acct_7F3@ID.Invalid"); err != nil || got != "acct_7f3@id.invalid" {
		t.Errorf("normIdentity = (%q, %v), want (acct_7f3@id.invalid, nil)", got, err)
	}
	for _, s := range []string{"", "nolocal", "@x.io", "a@", "x y@id.invalid"} {
		if _, err := normIdentity(s); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("normIdentity(%q) error = %v, want ErrInvalidIdentity", s, err)
		}
	}
}

func TestNormDomain(t *testing.T) {
	good := []struct{ in, want string }{
		{"id.invalid", "id.invalid"},
		{"ID.Hostcheck.Invalid", "id.hostcheck.invalid"},
		{"  id.example  ", "id.example"},
	}
	for _, c := range good {
		got, err := normDomain(c.in)
		if err != nil || got != c.want {
			t.Errorf("normDomain(%q) = (%q, %v), want (%q, nil)", c.in, got, err, c.want)
		}
	}
	for _, s := range []string{"", "has@at", "has space", "sl/ash", "ha#sh", "nul\x00"} {
		if _, err := normDomain(s); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("normDomain(%q) error = %v, want ErrInvalidIdentity", s, err)
		}
	}
}

func TestNormPurposeSet(t *testing.T) {
	got, err := normPurposeSet([]string{"Login", " contact ", "login", "BILLING", "contact"})
	if err != nil {
		t.Fatalf("normPurposeSet error: %v", err)
	}
	want := []string{"login", "contact", "billing"} // deduped, first-seen order, lowercased
	if !slices.Equal(got, want) {
		t.Errorf("normPurposeSet = %v, want %v", got, want)
	}

	if got, err := normPurposeSet(nil); err != nil || got != nil {
		t.Errorf("normPurposeSet(nil) = (%v, %v), want (nil, nil)", got, err)
	}

	for _, bad := range [][]string{{""}, {"ok", "bad space"}, {"sl/ash"}, {"ha#sh"}, {"nul\x00"}} {
		if _, err := normPurposeSet(bad); !errors.Is(err, ErrInvalidPurpose) {
			t.Errorf("normPurposeSet(%v) error = %v, want ErrInvalidPurpose", bad, err)
		}
	}
}

// ----- Phase 1: construction & Identity formatter -----

func TestNewDefaultDomain(t *testing.T) {
	s, err := New("/userdb")
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if s.IdentityDomain() != DefaultIdentityDomain {
		t.Errorf("default domain = %q, want %q", s.IdentityDomain(), DefaultIdentityDomain)
	}
}

func TestNewOptionAndErrors(t *testing.T) {
	s, err := New("/userdb/", WithIdentityDomain("ID.Hostcheck.Invalid"))
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if s.IdentityDomain() != "id.hostcheck.invalid" {
		t.Errorf("domain = %q, want normalized id.hostcheck.invalid", s.IdentityDomain())
	}

	if _, err := New(""); err == nil {
		t.Error("New with empty keyspace should fail")
	}
	if _, err := New("relative/path"); err == nil {
		t.Error("New with relative keyspace should fail")
	}
	if _, err := New("/userdb", WithIdentityDomain("bad domain")); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("New with bad domain error = %v, want ErrInvalidIdentity", err)
	}
	if _, err := New("/userdb", WithIdentityDomain("")); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("New with empty domain error = %v, want ErrInvalidIdentity", err)
	}
}

func TestIdentity(t *testing.T) {
	s, err := New("/userdb", WithIdentityDomain("id.hostcheck.invalid"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Identity("Acct_7F3")
	if err != nil {
		t.Fatalf("Identity error: %v", err)
	}
	if want := "acct_7f3@id.hostcheck.invalid"; got != want {
		t.Errorf("Identity = %q, want %q", got, want)
	}

	// Result must itself be a valid identity.
	if _, err := normIdentity(got); err != nil {
		t.Errorf("Identity produced an invalid identity: %v", err)
	}

	for _, bad := range []string{"", "  ", "has@at", "has space", "sl/ash", "ha#sh", "nul\x00"} {
		if _, err := s.Identity(bad); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("Identity(%q) error = %v, want ErrInvalidIdentity", bad, err)
		}
	}

	// Default-domain Store.
	def, _ := New("/userdb")
	if got, err := def.Identity("u1"); err != nil || got != "u1@id.invalid" {
		t.Errorf("default Identity = (%q, %v), want (u1@id.invalid, nil)", got, err)
	}
}
