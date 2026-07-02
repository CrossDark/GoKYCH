package passkey

import (
	"net/url"
	"strings"
)

// NormalizeDomain turns an APP_DOMAIN value (bare host, host:port, or full
// origin with a scheme) into the (rpid, origin) pair the WebAuthn library
// expects. Returns ("", "") when the input can't be parsed so the caller can
// disable passkey rather than start with a broken RPID.
//
// Moved here from package main so the logic is reusable (and testable from
// the passkey package's own tests rather than main_test.go).
func NormalizeDomain(domain string) (rpid, origin string) {
	d := strings.TrimSpace(domain)
	if d == "" {
		return "", ""
	}
	// url.Parse needs a scheme to populate Host; otherwise it puts the
	// whole "host:port" into Path. Inject http:// when the caller omits
	// one (dev default is a bare domain / host:port).
	withScheme := d
	if !strings.Contains(d, "://") {
		withScheme = "http://" + d
	}
	u, err := url.Parse(withScheme)
	if err != nil || u.Host == "" {
		return "", ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	// Hostname() strips the port — that's exactly the bare RPID we want.
	return u.Hostname(), scheme + "://" + u.Host
}

// BuildOrigins expands a single primary origin (derived from APP_DOMAIN)
// into the full set the webauthn library will accept during
// FinishRegistration / FinishDiscoverableLogin. WebAuthn's origin check is
// byte-exact: scheme, host AND port must each equal clientDataJSON.origin.
// A bare APP_DOMAIN like "localhost" yields the primary origin
// "http://localhost", yet the user is almost always reaching the site
// through the Next.js dev server on :3000 (or the API on :8000) — with
// only the primary in the list, every ceremony fails with
// "Error validating origin".
//
// Strategy:
//   - Always include the primary origin as-is (production deployments set
//     APP_DOMAIN to the real origin and rely on this slot matching).
//   - For the special "localhost" host, add http(s)://localhost with the
//     common dev ports (3000, 8000, 8080) so the default dev setup works
//     without the operator having to remember "APP_DOMAIN=localhost:3000".
//   - For any other host, additionally add the same scheme + host with
//     ports 3000/8000/8080 too. These are unlikely in production (the
//     operator would set the real origin up front) and the cost is tiny;
//     a misconfigured :8000-only deployment still works. We deliberately
//     DON'T add cross-scheme (http://example.com when primary is
//     https://example.com) variants for non-localhost hosts — that would
//     silently allow plaintext-origin access on a production HTTPS site.
//   - For localhost only, also add the http↔https counterpart so a dev
//     box that mis-set APP_DOMAIN=https://localhost still works.
//
// Deduplicated and order-stable so the startup log line is readable.
func BuildOrigins(primary, rpid string) []string {
	scheme := "http"
	if strings.HasPrefix(primary, "https://") {
		scheme = "https"
	}
	// Dev ports we tolerate as alternative origins for the same host. Kept
	// short and explicit — these are the ports the project's own
	// docker-compose / next dev default to.
	devPorts := []string{"3000", "8000", "8080"}

	out := []string{primary}
	// localhost is special: it's never reachable over the public internet,
	// always HTTP in dev, and a dev box might be TLS-terminated locally, so
	// we don't gate the cross-scheme variant for it. For other hosts we
	// keep scheme strict to avoid weakening production HTTPS sites.
	schemes := []string{scheme}
	if rpid == "localhost" {
		if scheme == "http" {
			schemes = append(schemes, "https")
		} else {
			schemes = append(schemes, "http")
		}
	}
	seen := map[string]bool{primary: true}
	add := func(o string) {
		if o == "" || seen[o] {
			return
		}
		seen[o] = true
		out = append(out, o)
	}
	for _, sch := range schemes {
		// bare-host variant (no port) — already covered by primary when
		// APP_DOMAIN had no port; harmless to re-add once via dedup.
		add(sch + "://" + rpid)
		for _, p := range devPorts {
			add(sch + "://" + rpid + ":" + p)
		}
	}
	return out
}
