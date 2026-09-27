# userdb — Human-Email ⇆ Synthetic-Identity Directory

**Status:** Normative specification (v1 draft)
**Module:** `github.com/visvasity/userdb`
**Last updated:** 2026-09-26

This document is the normative specification for `userdb`, a small directory
library that maintains a **bidirectional mapping** between human-readable email
addresses and a stable **synthetic identity** email, with optional per-email
provider (identity-provider) provenance. It persists all state through the
`github.com/visvasity/kv` key-value API only, following the same conventions as
`github.com/visvasity/namedb`.

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **MAY**, and **OPTIONAL** in this
document are to be interpreted as described in RFC 2119.

---

## 1. Scope

### 1.1 Goals

1. Map **many** human-readable emails onto **one** synthetic identity email
   (many-to-one, forward direction), so that an application can address a
   subject by a stable name that is independent of which email the human
   currently uses.
2. Enumerate, for a synthetic identity, **all** human emails bound to it
   (one-to-many, reverse direction), each with **OPTIONAL** provider data
   describing the identity provider that vouched for that email.
3. Attach **OPTIONAL** application-defined **purpose tags** to each human email
   (e.g. `login`, `contact`, `billing`) and answer "which emails of this
   identity serve purpose P?" (§3.5).
4. Persist all state through the `kv.Database` interface, using
   caller-supplied `kv.Reader`/`kv.ReadWriter` so the operations compose inside
   the caller's own snapshot/transaction (as `namedb` does).
5. Stay minimal and free of application business logic: `userdb` is a naming
   directory, not an account store, an authentication system, or an
   authorization system.

### 1.2 Non-goals (explicitly out of scope)

1. **Authentication, OAuth flows, sessions, and account lifecycle.** `userdb`
   records a mapping; it does not verify emails, run login flows, or own an
   account record. The embedding application establishes identity and decides
   *when* a mapping is trustworthy (§9).
2. **Authorization.** `userdb` answers "what is the synthetic identity for this
   email?" and "which emails belong to this identity?"; it never answers "may
   this subject do X". Authorization belongs to a separate library (e.g.
   `github.com/visvasity/zanzibar`), which consumes the synthetic identity as
   an opaque subject.
3. **Generating account identifiers.** `userdb` does not invent the unique
   local part of a synthetic identity (the account id); the caller supplies it.
   `userdb` *does* own the identity **domain** convention: it provides an
   `Identity` helper (§10) that appends the Store's configured identity domain
   (§9.4) to a caller-supplied local part, so the domain lives in exactly one
   place. Guaranteeing the local part is unique remains the caller's
   responsibility.
4. **A login/audit log.** The **OPTIONAL** `Provider` attached to a mapping is
   advisory provenance, not an authoritative record of every login. Full
   per-login history, if needed, belongs to the application's account store.

---

## 2. Terminology

- **Human email** — a human-readable email address a person uses to sign in or
  be contacted, e.g. `alice@gmail.com`. It **MAY** change over time and **MAY**
  differ across identity providers.
- **Synthetic identity** (or **identity**) — a stable, application-assigned
  email-shaped string that names a single subject for the lifetime of that
  subject, e.g. `acct_7f3@id.example.invalid`. It is the value other systems
  (authorization, grants) reference.
- **Provider** — an identity provider that asserted a human email, described by
  a name (`google`, `github`, …), an OPTIONAL opaque provider subject, and a
  verified flag. Provider data is **OPTIONAL** on every mapping.
- **Purpose tag** — an OPTIONAL, application-defined label on a human email
  naming a use it is eligible for (e.g. `login`, `contact`, `billing`).
  `userdb` treats tags as opaque strings and does not enumerate them.
- **Link** — a stored binding of one human email to one synthetic identity,
  together with its OPTIONAL provider data and OPTIONAL purpose tags.
- **Store** — an exclusive `kv` key prefix (keyspace) under which one directory
  keeps all of its records; see §5.

---

## 3. Data model

### 3.1 Cardinality

1. Each human email **MUST** map to **at most one** synthetic identity. The
   forward mapping `email → identity` is therefore a partial function.
2. Each synthetic identity **MAY** have **zero or more** human emails bound to
   it. The reverse mapping `identity → {emails}` is one-to-many.
3. Consequently multiple human emails **MAY** resolve to the same synthetic
   identity; a single human email **MUST NOT** resolve to two identities at the
   same time. An attempt to bind an already-bound email to a different identity
   is a conflict (§7.1, §8).

### 3.2 Human email

A human email is an opaque, case-folded string (§4). `userdb` does not parse
its structure beyond the validation in §4 and does not attempt delivery.

### 3.3 Synthetic identity

A synthetic identity is an opaque, case-folded, email-shaped string. The caller
supplies the local part (an account id); the `Identity` helper (§10) forms the
full address by appending the Store's configured identity domain (§9.4). Callers
MAY also supply a fully-formed identity string directly. Once stored, `userdb`
treats the identity as an opaque key.

### 3.4 Provider

`Provider` carries OPTIONAL provenance for a single `Link`:

- `Name` — the identity-provider identifier (e.g. `google`, `github`). MAY be
  empty when the link was created administratively with no provider.
- `Subject` — the provider's stable, opaque subject identifier for the human
  (the OIDC `sub`). MAY be empty.
- `Verified` — true iff the provider asserted the email address is verified.

A `Link` carries **at most one** `Provider`. If the same human email is
asserted by more than one provider, the most recent `Link` call for that email
updates the provider data (last-writer-wins, §7.1). Applications that require a
per-provider audit trail MUST keep that in their own account store; it is a
non-goal here (§1.2.4).

### 3.5 Purpose tags

A human email **MAY** carry a set of purpose tags — opaque,
application-defined labels naming uses the email is eligible for.

1. Tags are a **set** per human email: order is insignificant and duplicates
   are collapsed (§4).
2. Tags are **many-to-many** with respect to emails: several emails of one
   identity **MAY** share a tag (e.g. two `contact` addresses), and one email
   **MAY** carry several tags.
3. `userdb` does **not** define, enumerate, or interpret the tag vocabulary;
   the embedding application owns and documents it (as `namedb` treats its
   `datatype`).
4. Tags are **descriptive metadata only**. `userdb` records that an email is
   eligible for a purpose; it never acts on a purpose (it does not send mail,
   pick a "primary", or enforce that a purpose is served by exactly one email).
   Selecting *which* tagged email to use for an action is the application's
   decision. (A future revision MAY add an OPTIONAL primary-per-purpose
   designation; it is deliberately excluded from v1.)

---

## 4. Normalization and validation

1. Before use as a key, both a human email and a synthetic identity **MUST** be
   normalized by trimming surrounding ASCII whitespace and lowercasing using
   Unicode simple case folding for the ASCII range (the `local@domain` form is
   lowercased in full). Normalization is idempotent.
2. A human email or synthetic identity is **valid** iff, after normalization,
   it is non-empty, contains exactly one `@`, has a non-empty local part and a
   non-empty domain part, and contains no ASCII control characters, spaces,
   `/`, `#`, or NUL bytes. Invalid inputs are rejected with `ErrInvalidEmail`
   (human email) or `ErrInvalidIdentity` (synthetic identity) (§8).
3. All lookups and writes normalize their inputs first; callers need not
   pre-normalize, and a value stored under one casing is found under any
   casing.
4. A **purpose tag** is normalized by trimming surrounding ASCII whitespace and
   lowercasing the ASCII range. A tag is **valid** iff, after normalization, it
   is non-empty and contains no ASCII control characters, spaces, `/`, `#`, or
   NUL bytes. Invalid tags are rejected with `ErrInvalidPurpose` (§8). Within a
   single email the normalized tag set is deduplicated.
5. The **identity domain** configured on a Store (§9.4) is normalized (trim,
   lowercase) and validated **once at construction**: it MUST be non-empty,
   contain no `@`, and contain no ASCII control characters, spaces, `/`, `#`, or
   NUL bytes (i.e. it is a valid domain part). An invalid domain fails `New`
   with `ErrInvalidIdentity`. A **local part** passed to the `Identity` helper
   (§10) MUST likewise be non-empty and contain none of `@`, control, space,
   `/`, `#`, or NUL; the helper joins them as `local@domain` and the result MUST
   satisfy the identity validity rule (§4.2), else it fails with
   `ErrInvalidIdentity`.

---

## 5. Persistence layout

All records live under the Store's keyspace (an exclusive, non-empty key prefix;
any trailing `/` is ignored). Two disjoint key classes are used; both are written
within the same `kv.ReadWriter` so the two directions never drift:

| Class | Key | Value (gob) | Meaning |
|-------|-----|-------------|---------|
| forward | `<keyspace>/e/<email>` | `{Synthetic, Provider?, Purposes?}` | the identity this email maps to, plus its provider data and purpose tags |
| reverse | `<keyspace>/s/<identity>` | `{Emails: []Link}` | every email bound to this identity, denormalized with provider data and purpose tags |

Notes:

1. `<email>` and `<identity>` are the normalized (§4) strings.
2. The forward record is the source of truth for a single email's provider
   data and purpose tags. The reverse record is **denormalized**: it stores the
   full `Link` (identity, provider, purposes) for each bound email so `Emails`
   (§7.4) and `EmailsFor` (§7.8) are a single read. Every mutation (§7)
   **MUST** update both the forward and the reverse record atomically within
   the supplied `kv.ReadWriter` so the two classes remain consistent.
3. The value encoding is gob, consistent with `namedb`/`kvutil`.
4. The keyspace **MUST** be exclusive to one directory; no other component may
   write under it.

---

## 6. Consistency and transactions

1. Every operation takes a caller-supplied `kv.Reader` (read-only ops) or
   `kv.ReadWriter` (mutating ops); `userdb` performs no transaction management
   of its own. This lets callers batch a `userdb` mutation together with their
   own writes (e.g. creating an account and linking its first email) in one
   atomic transaction.
2. Read operations observe exactly the point-in-time view of the supplied
   reader. Mutating operations read-modify-write both records (§5) through the
   supplied read-writer; the caller's transaction provides atomicity and
   isolation.
3. `userdb` does not retry on serialization conflicts; the caller's transaction
   loop owns retry, exactly as with `namedb`.

---

## 7. Operations (normative semantics)

### 7.1 Link (create or update a forward mapping)

`Link(ctx, rw, email, synthetic, provider)` binds `email` to `synthetic`.

1. Both `email` and `synthetic` are normalized and validated (§4); invalid
   input fails with `ErrInvalidEmail` / `ErrInvalidIdentity`.
2. If `email` is currently bound to a **different** identity, `Link` **MUST**
   fail with `ErrConflict` and make no change. Re-binding requires an explicit
   `Unlink` first (§7.2), or a `Merge` (§7.5).
3. If `email` is unbound, `Link` **MUST** create the forward record and add the
   `Link` to the reverse record of `synthetic` (creating that reverse record if
   absent).
4. If `email` is already bound to the **same** identity, `Link` is idempotent
   with respect to the binding and **MUST** update the stored provider data to
   the supplied value (last-writer-wins, §3.4); it returns nil.
5. `provider` is OPTIONAL; a nil provider stores no provenance.

### 7.2 Unlink (remove one forward mapping)

`Unlink(ctx, rw, email)` removes the binding for `email`.

1. `email` is normalized (§4).
2. The forward record for `email` is deleted and `email` is removed from its
   identity's reverse record. Other emails bound to that identity are
   unaffected (this is the many-to-one contract, §3.1).
3. If the identity's reverse record becomes empty, it **MAY** be deleted.
4. Unlinking an email that is not bound **MUST** return `os.ErrNotExist`.

### 7.3 Resolve (forward: email → identity)

`Resolve(ctx, r, email)` returns the synthetic identity and OPTIONAL provider
data bound to `email`.

1. `email` is normalized (§4).
2. On a hit it returns `(synthetic, provider, nil)`; `provider` is nil when no
   provenance was stored.
3. On a miss it returns `("", nil, os.ErrNotExist)`.

### 7.4 Emails (reverse: identity → emails)

`Emails(ctx, r, synthetic)` returns every `Link` bound to `synthetic`.

1. `synthetic` is normalized (§4).
2. On a hit it returns the full list of `Link`s (each with its OPTIONAL
   provider and purpose tags). The order is unspecified but SHOULD be stable
   across reads that observe the same state.
3. A synthetic identity with no bound emails returns an empty slice and a nil
   error (it is not an error to ask about an identity that has no emails).

### 7.5 Merge (OPTIONAL — fold one identity into another)

`Merge(ctx, rw, from, to)` re-binds every email of `from` onto `to`. It exists
to support account merges (two identities discovered to be the same subject).

1. Both identities are normalized (§4).
2. Every email bound to `from` is re-bound to `to`, preserving its provider
   data; the `from` reverse record is deleted. If `from == to`, `Merge` is a
   no-op.
3. Merge is subject to the same conflict rule as §7.1 only across identities,
   not within: because each email had exactly one binding (to `from`), moving
   it to `to` cannot conflict; the result is that `to` accumulates the union of
   both identities' emails.
4. Purpose tags are carried over unchanged with each moved email.

### 7.6 Tag (add purpose tags to an email)

`Tag(ctx, rw, email, purposes...)` adds one or more purpose tags to a bound
email.

1. `email` is normalized (§4); each purpose is normalized and validated (§4.4),
   and an invalid purpose fails with `ErrInvalidPurpose` making no change.
2. If `email` is not bound, `Tag` **MUST** return `os.ErrNotExist`.
3. Tags are added to the email's set; adding a tag already present is a no-op.
   Both the forward and reverse records are updated within `rw` (§5).

### 7.7 Untag (remove purpose tags from an email)

`Untag(ctx, rw, email, purposes...)` removes one or more purpose tags from a
bound email.

1. `email` is normalized (§4); each purpose is normalized (§4.4).
2. If `email` is not bound, `Untag` **MUST** return `os.ErrNotExist`.
3. Removing a tag the email does not have is a no-op. Both records are updated
   within `rw` (§5).

### 7.8 EmailsFor (reverse, filtered by purpose)

`EmailsFor(ctx, r, synthetic, purpose)` returns the subset of the identity's
bound emails whose tag set contains `purpose`.

1. `synthetic` and `purpose` are normalized (§4).
2. It returns every matching `Link` (each with its provider and purpose tags);
   the order follows §7.4.2.
3. An identity with no matching email returns an empty slice and a nil error.
   `EmailsFor` never selects a single "primary" email; if several emails match,
   all are returned and the choice among them is the caller's (§3.5.4).

---

## 8. Errors

`userdb` defines these sentinel errors (comparable with `errors.Is`):

- `ErrInvalidEmail` — a human email failed validation (§4.2).
- `ErrInvalidIdentity` — a synthetic identity failed validation (§4.2).
- `ErrInvalidPurpose` — a purpose tag failed validation (§4.4).
- `ErrConflict` — the human email is already bound to a different synthetic
  identity (§7.1.2).

Missing-record conditions are reported as `os.ErrNotExist` (wrapped), and
"already exists, identical" conditions follow `namedb` conventions (idempotent
success, §7.1.4). Underlying `kv` errors are returned wrapped and unmodified in
meaning.

---

## 9. Security considerations

### 9.1 Trust is the caller's responsibility

`userdb` stores whatever the caller links. It does **not** verify emails.
Binding an unverified email to an identity that other systems trust (e.g. by
granting it access) is an **account-takeover vector**: an attacker who controls
an identity provider account for `victim@example.com`, or who can assert that
address unverified, could otherwise inherit the victim's synthetic identity.
Callers **MUST** only `Link` (or `Merge` on) an email they have independently
verified — in practice, a provider-verified email (`Provider.Verified == true`)
or an out-of-band confirmed address.

### 9.2 One email, one identity

The §3.1 invariant (an email maps to at most one identity) is what makes the
forward direction a well-defined function and prevents ambiguous grants.
Enforce it: when a second provider presents an already-bound verified email,
link to the **existing** identity (or `Merge`), never create a second identity
for the same email.

### 9.3 Purpose tags do not imply deliverability

A `contact` (or similar) purpose tag records only that the application *intends*
an email for that use; it is **not** proof the person controls that inbox.
`Provider.Verified` likewise means "the identity provider vouched for this
address at sign-in", which is a different assertion from "this person owns this
mailbox and agreed to receive our mail" — and a purpose-tagged email MAY have
been added administratively with no provider at all. Before sending sensitive
mail to a tagged address, the application **MUST** apply its own confirmation
policy; `userdb` stores the tag but performs no verification and sends no mail.

### 9.4 Synthetic-identity domain (configurable)

The **domain** part of a synthetic identity is a Store-level option, defaulting
to `id.invalid`. It is set with `WithIdentityDomain` (§10); the `Identity`
helper forms `<local>@<domain>` from a caller-supplied local part. The local
part SHOULD be an opaque, stable account identifier, and MUST NOT be derived
from the human email (deriving it would re-couple the identity to the very email
`userdb` exists to decouple).

The default `id.invalid` uses the RFC 6761 `.invalid` special-use TLD, which is
guaranteed never to resolve and never to be a real, deliverable domain — so a
synthetic identity can never collide with a human email, and a stray attempt to
mail one fails closed. Deployments SHOULD keep a domain with this property.
Recommended choices, strongest first:

1. **A subdomain of `.invalid`** (the default family), e.g.
   `id.<product>.invalid`. Zero DNS configuration; can never resolve.
2. **A real subdomain the deployment owns that publishes a null MX record**
   (RFC 7505, `MX 0 "."`). Branded and self-documenting, but requires correct,
   durable DNS and relies on never attaching a deliverable MX.

Deployments SHOULD NOT use `.localhost` (RFC 6761 loopback — it resolves to
`127.0.0.1`/`::1`, so a stray delivery or fetch becomes a loopback connection),
nor `.test`/`.example` (misleading semantics, and `example.*` resolves).

Changing the identity domain later is a **formatting change only**: it affects
identities newly formed by the `Identity` helper and does NOT rewrite identities
already stored, which remain valid opaque keys. Callers MUST persist an
account's identity once and reuse it, rather than re-deriving it on each use, so
that a domain change never orphans existing bindings.

---

## 10. Go API

The complete exported surface. Method receivers take a caller-supplied
`kv.Reader`/`kv.ReadWriter`, mirroring `namedb`.

```go
package userdb

import (
	"context"

	"github.com/visvasity/kv"
)

// Store is an exclusive key prefix under which one
// email⇆identity directory keeps all of its records. No other component may
// write under the same keyspace.
type Store struct { /* unexported */ }

// New returns a directory that persists under the given keyspace (an exclusive
// key prefix; any trailing "/" is ignored).
// It fails if keyspace is empty, or if an option is invalid
// (e.g. WithIdentityDomain given a malformed domain -> ErrInvalidIdentity).
func New(keyspace string, opts ...Option) (*Store, error)

// Option configures a Store at construction.
type Option func(*Store)

// WithIdentityDomain sets the domain part appended by Identity. It defaults to
// "id.invalid". The domain is validated by New (§4.5); see §9.4 for guidance on
// choosing a non-resolving, non-deliverable domain.
func WithIdentityDomain(domain string) Option

// Identity forms a synthetic identity "<local>@<domain>" from a caller-supplied
// local part (an opaque account id) and the Store's configured identity domain.
// It fails with ErrInvalidIdentity if local is malformed or the result is not a
// valid identity. Identity does not persist anything; the caller binds emails to
// the result with Link.
func (s *Store) Identity(local string) (string, error)

// IdentityDomain returns the Store's configured identity domain.
func (s *Store) IdentityDomain() string

// Provider is OPTIONAL provenance for a single Link: the identity provider that
// asserted the human email. All fields are optional; a zero Provider records
// only that some provider was involved.
type Provider struct {
	Name     string // identity-provider id, e.g. "google", "github"
	Subject  string // provider's stable opaque subject (OIDC "sub")
	Verified bool   // provider asserted the email address is verified
}

// Link is one human email bound to one synthetic identity, with OPTIONAL
// provider data and OPTIONAL purpose tags. Email and Synthetic are the
// normalized (lower-cased) forms; Purposes is a normalized, deduplicated set.
type Link struct {
	Email     string
	Synthetic string
	Provider  *Provider // nil when no provenance was stored
	Purposes  []string  // app-defined labels this email is eligible for; may be empty
}

// Link binds email to the synthetic identity (many emails MAY map to one
// identity). It fails with ErrConflict if email is already bound to a different
// identity; it is idempotent (updating provider data) if email is already bound
// to synthetic. provider MAY be nil. Both records (§5) are updated within rw.
func (s *Store) Link(ctx context.Context, rw kv.ReadWriter, email, synthetic string, provider *Provider) error

// Unlink removes the binding for email. Other emails bound to the same identity
// are unaffected. It returns os.ErrNotExist if email is not bound.
func (s *Store) Unlink(ctx context.Context, rw kv.ReadWriter, email string) error

// Resolve returns the synthetic identity and OPTIONAL provider data bound to
// email (the many-to-one, forward direction). It returns os.ErrNotExist if
// email is not bound.
func (s *Store) Resolve(ctx context.Context, r kv.Reader, email string) (synthetic string, provider *Provider, err error)

// Emails returns every human email bound to the synthetic identity, each with
// its OPTIONAL provider data and purpose tags (the one-to-many, reverse
// direction). An identity with no bound emails yields an empty slice and a nil
// error.
func (s *Store) Emails(ctx context.Context, r kv.Reader, synthetic string) ([]Link, error)

// Tag adds one or more purpose tags to a bound email. It returns os.ErrNotExist
// if email is not bound and ErrInvalidPurpose if any tag is invalid. Adding a
// tag already present is a no-op.
func (s *Store) Tag(ctx context.Context, rw kv.ReadWriter, email string, purposes ...string) error

// Untag removes one or more purpose tags from a bound email. It returns
// os.ErrNotExist if email is not bound. Removing an absent tag is a no-op.
func (s *Store) Untag(ctx context.Context, rw kv.ReadWriter, email string, purposes ...string) error

// EmailsFor returns the subset of the synthetic identity's bound emails whose
// tag set contains purpose. It never selects a single "primary"; if several
// match, all are returned. No match yields an empty slice and a nil error.
func (s *Store) EmailsFor(ctx context.Context, r kv.Reader, synthetic, purpose string) ([]Link, error)

// Merge re-binds every email of the from identity onto the to identity,
// preserving provider data and purpose tags, and deletes the from identity. It
// supports account merges. A from == to call is a no-op.
func (s *Store) Merge(ctx context.Context, rw kv.ReadWriter, from, to string) error

// Sentinel errors, comparable with errors.Is. Missing records are reported as
// os.ErrNotExist.
var (
	ErrInvalidEmail    error // a human email failed validation
	ErrInvalidIdentity error // a synthetic identity failed validation
	ErrInvalidPurpose  error // a purpose tag failed validation
	ErrConflict        error // email already bound to a different identity
)
```

---

## 11. Relationship to other modules

- **`namedb`** — same shape (a `Store` over an exclusive keyspace, caller-owned
  transactions, denormalized both-sides records). `namedb` maps human names to
  an `int64` id within a namespace; `userdb` maps human emails to an
  email-shaped synthetic identity and additionally carries provider provenance.
- **`zanzibar`** — the authorization consumer. It treats the synthetic identity
  as an opaque `user:<identity>` subject. The dependency is one-way:
  applications resolve `email → identity` via `userdb`, then grant/check via
  `zanzibar`. `zanzibar` MUST NOT depend on `userdb`.
