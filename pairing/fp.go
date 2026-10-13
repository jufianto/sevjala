package pairing

import "strings"

// FPChars is how much of its fingerprint a node shows next to the code: 64
// bits, out of reach for an impostor generating certificates.
const FPChars = 16

// ShortFP is the part of a "sha256:<hex>" fingerprint that 'sevjala code' shows.
func ShortFP(fp string) string {
	h := strings.TrimPrefix(fp, "sha256:")
	if len(h) > FPChars {
		h = h[:FPChars]
	}
	return h
}

// ParseFP normalises what the operator typed: an optional "sha256:", spaces,
// dashes and colons dropped, any case; at least FPChars hex characters.
func ParseFP(s string) (string, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "sha256:")
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == ':' {
			return -1
		}
		return r
	}, s)
	if len(s) < FPChars || len(s) > 64 || strings.Trim(s, "0123456789abcdef") != "" {
		return "", false
	}
	return s, true
}

// MatchFP reports whether fingerprint fp starts with a ParseFP result.
func MatchFP(fp, prefix string) bool {
	return strings.HasPrefix(strings.TrimPrefix(fp, "sha256:"), prefix)
}
