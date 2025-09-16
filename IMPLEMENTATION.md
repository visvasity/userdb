# userdb — Implementation Plan

**Status:** Implementation plan (v1)
**Module:** `github.com/visvasity/userdb`
**Companion:** [SPEC.md](./SPEC.md) (normative)
**Last updated:** 2026-09-26

This document describes *how* `userdb` is built, and in *what order*. It is not
normative — [SPEC.md](./SPEC.md) is. The build is phase-gated: each phase
compiles, ships with its own tests, and must satisfy a stated gate before the
next phase begins. The ordering is **pure → storage → forward core → reverse
read → tags → merge → hardening**, so the central invariant (SPEC §3.1: one
human email maps to at most one identity, and the forward and reverse records
never drift) is established early and every later phase builds on proven ground.

---

## 1. Package layout

Mirrors `github.com/visvasity/namedb` in spirit, with two deliberate
simplifications:

- **Raw keys, not hashed.** `namedb` md5-hashes its keys; `userdb` uses raw
  `e/<email>` and `s/<identity>` keys. The validated email/identity strings
  (SPEC §4) are already free of `/`, `#`, spaces, control bytes, and NUL, so
  raw keys are safe, human-debuggable, and prefix-scannable.
- **No `internal` package.** The on-disk gob structs stay unexported inside
  `package userdb`. They reference the public `Provider`/`Link` types, so
  hoisting them into `internal` would create an import cycle for no benefit at
  this size.

```
userdb/
  doc.go          // package documentation (SPEC in prose)
  userdb.go       // Store, New, Option, WithIdentityDomain, Identity,
                  //   IdentityDomain, Provider, Link, sentinel errors, all methods
  records.go      // unexported gob structs (emailRec, identityRec) + key builders
  validate.go     // normalize/validate for email, identity, domain, purpose (pure)
  userdb_test.go  // tests, grouped per phase
```

The identity **domain** is a Store option (`WithIdentityDomain`, default
`id.invalid`; SPEC §9.4). The `Identity(local)` helper forms `<local>@<domain>`
so the domain convention lives in one place; the caller still supplies the
unique local part (account id).

### go.mod housekeeping

- Add `github.com/visvasity/kvmemdb` as a test dependency (in-memory backend).
- `userdb` does **not** import `namedb`; `go mod tidy` will drop the current
  `namedb` requirement.
- Keep the existing `github.com/visvasity/kv` requirement.

---

## 2. On-disk model

Two disjoint key classes under the Store's absolute keyspace (SPEC §5). Both are
written within the same caller-supplied `kv.ReadWriter` on every mutation, so
they never drift.

| Class | Key | Value (gob) |
|-------|-----|-------------|
| forward (source of truth per email) | `<keyspace>/e/<email>` | `emailRec{Synthetic string; Provider *Provider; Purposes []string}` |
| reverse (denormalized for one-read enumeration) | `<keyspace>/s/<identity>` | `identityRec{Emails []Link}` |

The reverse record denormalizes each email's provider and purpose tags so
`Emails`/`EmailsFor` are a single read; the mutation helpers keep it in step
with the forward records.

---

## 3. Transaction model

Every method takes a caller-supplied `kv.Reader` (reads) or `kv.ReadWriter`
(mutations), exactly as `namedb` does. `userdb` performs no transaction
management and no serialization-conflict retry — the caller's transaction owns
atomicity, isolation, and retry. This lets a `userdb` mutation be batched
atomically with the caller's own writes (e.g. create-account + link-first-email
in one transaction).

Tests drive this by wrapping operations in a `kv` transaction over
`kv.DatabaseFrom(kvmemdb.New())`.

---

## 4. Phases

### Phase 1 — Normalization & validation (pure, no I/O)

**Build:** `validate.go` — `normalizeEmail`/`validateEmail`,
`normalizeIdentity`/`validateIdentity`, `normalizeDomain`/`validateDomain`,
`normalizePurpose`, and a purpose-set normalizer (trim, lowercase, validate,
deduplicate). Plus the `Store` construction (`New`, `Option`,
`WithIdentityDomain` defaulting to `id.invalid`) and the pure `Identity(local)`
formatter that joins `local@domain` and validates the result.

**Tests:** table-driven — casing folds, surrounding-whitespace trim, the reject
set (`/`, `#`, space, ASCII control, NUL), exactly-one-`@`, non-empty local and
domain parts, empty/blank inputs, and idempotency (normalizing twice is a
no-op). For construction/formatting: default domain is `id.invalid`; `New` fails
with `ErrInvalidIdentity` on a malformed `WithIdentityDomain`; `Identity`
rejects a bad local part and produces a valid `<local>@<domain>` otherwise.

**Gate:** all pure functions (including `Identity`) pass before any `kv` code is
written.

### Phase 2 — Persistence primitives & record model

**Build:** key builders (`emailKey`, `identityKey`); the `emailRec` and
`identityRec` gob structs; thin `getGob`/`setGob`/`del` wrappers over `kvutil`;
and the single read-modify-write-both-records helper that every mutation
funnels through.

**Tests:** round-trip an `emailRec` and an `identityRec` through `kvmemdb`;
confirm a missing key surfaces as `os.ErrNotExist`.

**Gate:** storage layer round-trips cleanly.

### Phase 3 — Forward core: `Link` / `Unlink` / `Resolve` *(the heart)*

**Build:**
- `Link` — normalize+validate; read the forward record; if bound to a
  *different* identity fail with `ErrConflict` and change nothing; if unbound,
  create the forward record and add the `Link` to the reverse record; if bound
  to the *same* identity, update provider data (last-writer-wins) while
  **preserving** existing purpose tags (SPEC §7.1.4).
- `Unlink` — remove the forward record and the email's reverse entry; delete an
  emptied reverse record; `os.ErrNotExist` if the email is not bound.
- `Resolve` — forward read → `(synthetic, provider)`; `os.ErrNotExist` on miss.

**Tests:** many-emails→one-identity; `ErrConflict` on rebind-to-different;
idempotent same-identity re-link; unlink-one-leaves-siblings; unlink-unbound →
`os.ErrNotExist`; case-insensitive resolve; and a both-sides-consistency
assertion run after **every** mutating op (every forward record has a matching
reverse entry and vice versa).

**Gate:** the SPEC §3.1 invariant holds under all Phase-3 operations.

### Phase 4 — Reverse enumeration: `Emails`

**Build:** `Emails` — single reverse read → copy of `[]Link`; empty slice + nil
error for an identity with no bound emails.

**Tests:** enumerate all bound emails; provider data preserved; empty identity
yields `(nil-or-empty, nil)`; cross-check that every returned `Link.Email`
resolves back to the same identity (no denormalization drift).

**Gate:** reverse view matches the forward records exactly.

### Phase 5 — Purpose tags: `Tag` / `Untag` / `EmailsFor`

**Build:** the `Link.Purposes` field; `Tag`/`Untag` update both the forward and
reverse records; `EmailsFor` filters the reverse list by a normalized purpose.

**Tests:** tag then read back via `Emails`; `Untag` removes; `os.ErrNotExist`
when tagging an unbound email; `ErrInvalidPurpose` on a bad label; add/remove
idempotency; many-to-many (two emails of one identity share a tag, both
returned by `EmailsFor`); purposes survive an idempotent re-`Link`; `EmailsFor`
returns *all* matches and never selects a single "primary".

**Gate:** tags round-trip on both record sides and filtering is correct.

### Phase 6 — `Merge`

**Build:** `Merge(from, to)` — enumerate `from`'s emails; re-point each forward
record to `to` carrying its provider and purposes; append them to `to`'s
reverse record; delete `from`'s reverse record; `from == to` is a no-op.

**Tests:** `to` ends with the union of both identities' emails; moved emails now
`Resolve` to `to`; provider data and purpose tags preserved; `from` reverse
record deleted; merge into an empty target; `from == to` no-op.

**Gate:** merge preserves the §3.1 invariant and all attached data.

### Phase 7 — Hardening & docs

**Build:** `doc.go` with runnable examples; final `gofmt`/`go vet` pass.

**Tests:**
- A randomized op-sequence test (deterministic seed) applying a mix of
  `Link`/`Unlink`/`Tag`/`Untag`/`Merge`, then asserting global invariants:
  forward↔reverse bijection, each email bound to at most one identity, and
  equal purpose sets on both record sides.
- A transaction-composition test: a `userdb` mutation batched with a caller
  write in one transaction commits/aborts atomically together.
- The documented `kvmemdb` concurrent-`Link` caveat (blind writes to a
  previously-absent key are not serialized), noted the same way as in zanzibar.

**Gate:** full suite, `go vet`, and `gofmt` all green.

---

## 5. Review gates

Implementation proceeds one phase at a time. After each phase the work stops for
review; a phase begins only after the previous phase's gate is approved. Phases
3–4 alone constitute a shippable minimal directory (bidirectional
email⇆identity mapping); tags (Phase 5) and merge (Phase 6) are strictly
additive and may be deferred without reworking the core.

---

## 6. Out of scope for v1

Consistent with SPEC §1.2 and §3.5.4:

- Generating the unique account-id local part (the caller supplies it; `userdb`
  only appends the configured domain via `Identity`, §9.4).
- A primary-per-purpose designation (`SetPrimary`/`Primary`) — deferred; the
  reverse record's shape leaves room to add it later without migration.
- Email verification, message delivery, login/audit history, and any
  authorization decision.
