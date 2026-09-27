// Copyright (c) 2026 Visvasity LLC

package userdb

import "strings"

// asciiSpaces is the set of ASCII whitespace bytes trimmed during normalization
// (SPEC §4.1). Unicode whitespace outside this set is left untouched.
const asciiSpaces = " \t\n\r\v\f"

// normalize trims surrounding ASCII whitespace and lower-cases the ASCII range
// (A-Z). Non-ASCII bytes are preserved. Normalization is idempotent (SPEC §4).
func normalize(s string) string {
	s = strings.Trim(s, asciiSpaces)
	var b []byte
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			if b == nil {
				b = []byte(s)
			}
			b[i] = c + ('a' - 'A')
		}
	}
	if b == nil {
		return s
	}
	return string(b)
}

// isBadByte reports whether b is disallowed everywhere in userdb keys and tags:
// an ASCII control byte (including NUL), an ASCII space, '/', or '#'
// (SPEC §4.2, §4.4, §4.5). Bytes >= 0x80 (UTF-8 continuation/lead) are allowed.
func isBadByte(b byte) bool {
	return b < 0x20 || b == 0x7f || b == ' ' || b == '/' || b == '#'
}

// hasBadByte reports whether s contains any byte rejected by isBadByte.
func hasBadByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if isBadByte(s[i]) {
			return true
		}
	}
	return false
}

// splitAddress splits an email-shaped string into local and domain parts,
// requiring exactly one '@' with non-empty parts on both sides.
func splitAddress(s string) (local, domain string, ok bool) {
	i := strings.IndexByte(s, '@')
	if i < 0 {
		return "", "", false
	}
	local, domain = s[:i], s[i+1:]
	if local == "" || domain == "" {
		return "", "", false
	}
	if strings.IndexByte(domain, '@') >= 0 { // more than one '@'
		return "", "", false
	}
	return local, domain, true
}

// isValidAddress reports whether s is a valid email-shaped value: non-empty,
// free of bad bytes, with exactly one '@' and non-empty local and domain parts
// (SPEC §4.2).
func isValidAddress(s string) bool {
	if s == "" || hasBadByte(s) {
		return false
	}
	_, _, ok := splitAddress(s)
	return ok
}

// isValidLocalPart reports whether s is usable as the local part of a synthetic
// identity: non-empty, no '@', and no bad bytes (SPEC §4.5).
func isValidLocalPart(s string) bool {
	return s != "" && strings.IndexByte(s, '@') < 0 && !hasBadByte(s)
}

// isValidDomain reports whether s is usable as the identity domain: non-empty,
// no '@', and no bad bytes (SPEC §4.5).
func isValidDomain(s string) bool {
	return s != "" && strings.IndexByte(s, '@') < 0 && !hasBadByte(s)
}

// normEmail normalizes and validates a human email (SPEC §4.1, §4.2).
func normEmail(s string) (string, error) {
	s = normalize(s)
	if !isValidAddress(s) {
		return "", ErrInvalidEmail
	}
	return s, nil
}

// normIdentity normalizes and validates a synthetic identity (SPEC §4.1, §4.2).
func normIdentity(s string) (string, error) {
	s = normalize(s)
	if !isValidAddress(s) {
		return "", ErrInvalidIdentity
	}
	return s, nil
}

// normDomain normalizes and validates an identity domain (SPEC §4.5).
func normDomain(s string) (string, error) {
	s = normalize(s)
	if !isValidDomain(s) {
		return "", ErrInvalidIdentity
	}
	return s, nil
}

// normPurpose normalizes and validates a single purpose tag (SPEC §4.4).
func normPurpose(s string) (string, error) {
	s = normalize(s)
	if s == "" || hasBadByte(s) {
		return "", ErrInvalidPurpose
	}
	return s, nil
}

// normPurposeSet normalizes, validates, and deduplicates a set of purpose tags,
// preserving first-seen order (SPEC §4.4). A nil/empty input yields nil.
func normPurposeSet(ps []string) ([]string, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(ps))
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		n, err := normPurpose(p)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}
