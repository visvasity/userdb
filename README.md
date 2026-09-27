# userdb

A small directory that maps **human email addresses ⇆ a stable synthetic
identity**, so the rest of your system can name a user by something that never
changes — even as their login email or identity provider does.

- Forward (many-to-one): many human emails resolve to one synthetic identity.
- Reverse (one-to-many): a synthetic identity enumerates all its human emails.
- Optional per-email **provider** provenance and **purpose tags** (`login`,
  `contact`, `billing`, …).

Persistence is through the [`kv`](https://github.com/visvasity/kv) API only;
every method takes a caller-supplied `kv.Reader`/`kv.ReadWriter`, so a `userdb`
change commits atomically inside your own transaction. See
[SPEC.md](./SPEC.md) for the normative details.

## Why this package exists

If you key accounts by the OAuth pair `(provider, subject)` or by the email a
provider hands you, you hit two problems:

1. **The same person signs in with two providers** (Google and GitHub) and gets
   **two accounts**, even with the same email.
2. **A person's email changes** at the provider, and every grant, ACL entry, or
   record that referenced that email is now orphaned.

`userdb` fixes both by introducing one **synthetic identity** per person — an
opaque, email-shaped string like `acct_7f3@id.invalid` — and mapping every human
email (from every provider) onto it. Authorization, grants, audit records, and
anything else reference the synthetic identity, which is stable for the lifetime
of the account. Human emails and providers become interchangeable spokes; the
synthetic identity is the hub.

The default identity domain is `id.invalid` (an RFC 6761 special-use TLD that can
never resolve or receive mail), so a synthetic identity can never collide with a
real address. Configure it with `WithIdentityDomain`.

`userdb` deliberately does **not** do authentication, run OAuth flows, verify
emails, send mail, or make authorization decisions. It is only the directory.

## Install

```
go get github.com/visvasity/userdb
```

## Using it in an OAuth workflow

The typical flow: your OAuth callback verifies the provider assertion, then asks
`userdb` for the caller's synthetic identity (creating the binding on first
sign-in). Everything downstream uses that identity.

```go
// After the OAuth callback has verified the provider's response:
//   provider = "google", subject = "<oidc sub>", email = "alice@example.com",
//   emailVerified = true

func (a *App) onOAuthCallback(ctx context.Context, provider, subject, email string, emailVerified bool) (identity string, err error) {
	// SECURITY: only ever bind a verified email. Binding an unverified address
	// an attacker controls would let them inherit someone else's identity.
	if !emailVerified {
		return "", fmt.Errorf("refusing to bind unverified email")
	}

	tx, err := a.db.NewTransaction(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	switch id, _, err := a.users.Resolve(ctx, tx, email); {
	case err == nil:
		// Returning user: this email already maps to an identity.
		identity = id
	case errors.Is(err, os.ErrNotExist):
		// First time we've seen this email. Mint a new account id and identity.
		identity, err = a.users.Identity(a.newAccountID()) // e.g. "acct_7f3" -> acct_7f3@id.invalid
		if err != nil {
			return "", err
		}
	default:
		return "", err
	}

	// Bind (idempotent on repeat logins) and record the provider provenance.
	prov := &userdb.Provider{Name: provider, Subject: subject, Verified: true}
	if err := a.users.Link(ctx, tx, email, identity, prov); err != nil {
		return "", err // ErrConflict if this email already belongs to a different identity
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return identity, nil
}
```

Now your session stores `identity`, and authorization uses it as the subject —
for example with [`zanzibar`](https://github.com/visvasity/zanzibar):

```go
// The ACL subject is the stable synthetic identity, never the human email.
allowed, _ := acl.Check(ctx, &zanzibar.CheckRequest{
	Object:   "service:hostcheck",
	Relation: "staff",
	Subject:  "user:" + identity,
})
```

### Same person, second provider

When Alice later signs in with GitHub using the same verified email, `Resolve`
finds her existing identity and `Link` just records the new provenance — one
account, two providers. If the second provider brings a *different* verified
email, `Link` it to the same identity too; many emails may map to one identity.

If two separate accounts turn out to be the same person, fold one into the other:

```go
// Move every email of `from` onto `to`, then delete `from`.
err := users.Merge(ctx, tx, fromIdentity, toIdentity)
```

### Purpose tags

Tag which emails serve which purpose, and look them up:

```go
_ = users.Tag(ctx, tx, "alice@work.io", "contact", "billing")

// "Which address do we email for billing?" (returns all tagged; you pick.)
contacts, _ := users.EmailsFor(ctx, snap, identity, "billing")
```

A tag is descriptive metadata only — `userdb` never sends mail or picks a
"primary". A `contact` tag and `Provider.Verified` are **not** proof the person
owns that inbox; run your own confirmation before sending sensitive mail.

## API at a glance

| Method | Direction | Purpose |
|--------|-----------|---------|
| `Identity(local)` | — | Format `<local>@<domain>` for a new account id |
| `Link(email, identity, provider)` | forward | Bind an email (idempotent; `ErrConflict` on a different identity) |
| `Resolve(email)` | forward | Email → identity (+ provider); `os.ErrNotExist` if unbound |
| `Unlink(email)` | forward | Remove one email's binding |
| `Emails(identity)` | reverse | Identity → all bound emails |
| `Tag` / `Untag` / `EmailsFor` | — | Manage and query purpose tags |
| `Merge(from, to)` | — | Fold one identity's emails into another |

## Security notes

- **Only bind verified emails.** `Link`/`Merge` on an unverified address is an
  account-takeover vector.
- **One email → one identity.** `userdb` enforces this; when a second provider
  presents a known verified email, link to the existing identity (or `Merge`),
  never create a second one.
- **Keep the identity domain non-deliverable** (`.invalid`, or a domain you own
  with a null MX). Never use `.localhost`. See SPEC §9.4.

## License

Copyright (c) 2026 Visvasity LLC.
