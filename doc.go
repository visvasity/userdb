// Copyright (c) 2026 Visvasity LLC

// Package userdb maintains a bidirectional directory between human-readable
// email addresses and a stable synthetic identity email, with optional
// per-email provider provenance and optional purpose tags.
//
// The forward mapping is many-to-one: many human emails MAY resolve to one
// synthetic identity, but a single human email maps to at most one identity.
// The reverse mapping is one-to-many: an identity enumerates all human emails
// bound to it. See SPEC.md for the normative specification.
//
// A synthetic identity is an opaque, email-shaped string. The caller supplies
// its unique local part (an account id); the Identity helper appends the
// Store's configured identity domain (WithIdentityDomain, default
// "id.invalid"). userdb never mints the local part, verifies emails, sends
// mail, or makes authorization decisions.
//
// All persistence flows through the github.com/visvasity/kv API. Every method
// takes a caller-supplied kv.Reader or kv.ReadWriter, so a userdb mutation
// composes atomically inside the caller's own transaction. userdb performs no
// transaction management and no serialization-conflict retry; the caller's
// transaction owns atomicity, isolation, and retry.
//
// Concurrency caveat: with backends whose transactions do not record a read on
// a missing key (notably github.com/visvasity/kvmemdb), two concurrent
// transactions that each Link the *same, previously-unbound* email may both
// commit without a conflict, leaving a last-writer-wins binding rather than an
// ErrConflict. Update paths (an email already bound) read an existing record and
// are serialized normally. Applications that require first-writer-wins on brand
// new emails should serialize such creates themselves or use a backend that
// records missing-key reads.
package userdb
