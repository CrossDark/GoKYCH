package passkey

import (
	"slices"
	"testing"
)

// TestNormalizeDomain locks in the (rpid, origin) derivation for every
// accepted APP_DOMAIN form. The previous implementation (when this logic
// lived in package main) produced a double "http://http://…" prefix for the
// explicit http:// case and left the port stuck onto the RPID for
// "host:port" input, which silently broke passkey in the default
// docker-compose setup (frontend on :3000).
func TestNormalizeDomain(t *testing.T) {
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
		gotRPID, gotOrigin := NormalizeDomain(c.in)
		if gotRPID != c.wantRPID || gotOrigin != c.wantOrigin {
			t.Errorf("NormalizeDomain(%q) = (%q, %q); want (%q, %q)",
				c.in, gotRPID, gotOrigin, c.wantRPID, c.wantOrigin)
		}
	}
}

// TestBuildOrigins locks in the origin-set derivation. WebAuthn's origin
// check is byte-exact (scheme+host+port), so the dev server on :3000 needs
// http://localhost:3000 to be in the accepted list even when APP_DOMAIN
// was the bare "localhost". Production hosts keep strict same-scheme
// matching to avoid silently allowing http://example.com on a https site.
// We compare as a set (slices.Equal is order-sensitive, so we sort first)
// — the implementation's order is documented for the startup log, not
// part of the contract.
func TestBuildOrigins(t *testing.T) {
	cases := []struct {
		name    string
		primary string
		rpid    string
		want    []string
	}{
		{
			name:    "bare localhost pulls in dev ports + https peer",
			primary: "http://localhost", rpid: "localhost",
			want: []string{
				"http://localhost", "https://localhost",
				"http://localhost:3000", "http://localhost:8000", "http://localhost:8080",
				"https://localhost:3000", "https://localhost:8000", "https://localhost:8080",
			},
		},
		{
			name:    "localhost:3000 primary dedups the :3000 variant",
			primary: "http://localhost:3000", rpid: "localhost",
			want: []string{
				"http://localhost:3000", "http://localhost", "https://localhost",
				"http://localhost:8000", "http://localhost:8080",
				"https://localhost:3000", "https://localhost:8000", "https://localhost:8080",
			},
		},
		{
			name:    "production https host keeps same scheme, no http peer",
			primary: "https://kych.example.com", rpid: "kych.example.com",
			want: []string{
				"https://kych.example.com",
				"https://kych.example.com:3000", "https://kych.example.com:8000", "https://kych.example.com:8080",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildOrigins(c.primary, c.rpid)
			if len(got) != len(c.want) {
				t.Fatalf("BuildOrigins(%q,%q) returned %d items (%v); want %d (%v)",
					c.primary, c.rpid, len(got), got, len(c.want), c.want)
			}
			gotSorted := append([]string(nil), got...)
			wantSorted := append([]string(nil), c.want...)
			slices.Sort(gotSorted)
			slices.Sort(wantSorted)
			if !slices.Equal(gotSorted, wantSorted) {
				t.Errorf("BuildOrigins(%q,%q) set mismatch:\n got  %v\n want %v",
					c.primary, c.rpid, got, c.want)
			}
		})
	}
}
