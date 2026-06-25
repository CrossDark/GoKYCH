package main

import (
	"slices"
	"testing"
)

// TestNormalizeWebAuthnDomain locks in the (rpid, origin) derivation for
// every accepted APP_DOMAIN form. The previous implementation produced a
// double "http://http://…" prefix for the explicit http:// case and left
// the port stuck onto the RPID for "host:port" input, which silently broke
// passkey in the default docker-compose setup (frontend on :3000).
func TestNormalizeWebAuthnDomain(t *testing.T) {
	cases := []struct {
		in         string
		wantRPID   string
		wantOrigin string
	}{
		{"localhost", "localhost", "http://localhost"},
		{"localhost:3000", "localhost", "http://localhost:3000"},
		{"example.com", "example.com", "http://example.com"},
		{"example.com:8080", "example.com", "http://example.com:8080"},
		{"https://example.com", "example.com", "https://example.com"},
		{"http://localhost:3000", "localhost", "http://localhost:3000"},
		{"https://kych.example.com:8443", "kych.example.com", "https://kych.example.com:8443"},
		{"  ", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		gotRPID, gotOrigin := normalizeWebAuthnDomain(c.in)
		if gotRPID != c.wantRPID || gotOrigin != c.wantOrigin {
			t.Errorf("normalizeWebAuthnDomain(%q) = (%q, %q); want (%q, %q)",
				c.in, gotRPID, gotOrigin, c.wantRPID, c.wantOrigin)
		}
	}
}

// TestBuildWebAuthnOrigins covers the origin-expansion logic that turns a
// single APP_DOMAIN into the full list the webauthn lib accepts. The bug
// we're guarding against: with only the primary origin in RPOrigins, every
// ceremony from the Next.js dev server (http://localhost:3000) fails with
// "Error validating origin" because WebAuthn's check is byte-exact.
//
// Cases:
//   - localhost over http → primary + http+https × {bare, :3000, :8000, :8080}
//   - localhost over https → same set (https primary + http peer)
//   - real production domain over https → primary + dev-port siblings,
//     but NO http cross-scheme variant (would silently weaken HTTPS).
func TestBuildWebAuthnOrigins(t *testing.T) {
	cases := []struct {
		name    string
		primary string
		rpid    string
		want    []string
	}{
		{
			name:    "localhost http — primary + dev ports + https peer",
			primary: "http://localhost",
			rpid:    "localhost",
			want: []string{
				"http://localhost",
				"http://localhost:3000",
				"http://localhost:8000",
				"http://localhost:8080",
				"https://localhost",
				"https://localhost:3000",
				"https://localhost:8000",
				"https://localhost:8080",
			},
		},
		{
			name:    "localhost https — primary + dev ports + http peer",
			primary: "https://localhost",
			rpid:    "localhost",
			want: []string{
				"https://localhost",
				"https://localhost:3000",
				"https://localhost:8000",
				"https://localhost:8080",
				"http://localhost",
				"http://localhost:3000",
				"http://localhost:8000",
				"http://localhost:8080",
			},
		},
		{
			name:    "production https — primary + same-scheme dev ports only (no http cross-scheme)",
			primary: "https://example.com",
			rpid:    "example.com",
			want: []string{
				"https://example.com",
				"https://example.com:3000",
				"https://example.com:8000",
				"https://example.com:8080",
			},
		},
		{
			name:    "primary with port — primary stays first, dev-port variants still added",
			primary: "http://localhost:3000",
			rpid:    "localhost",
			want: []string{
				"http://localhost:3000",
				"http://localhost",
				"http://localhost:8000",
				"http://localhost:8080",
				"https://localhost",
				"https://localhost:3000",
				"https://localhost:8000",
				"https://localhost:8080",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildWebAuthnOrigins(c.primary, c.rpid)
			if !slices.Equal(got, c.want) {
				t.Errorf("buildWebAuthnOrigins(%q, %q):\n got  %v\n want %v",
					c.primary, c.rpid, got, c.want)
			}
		})
	}
}