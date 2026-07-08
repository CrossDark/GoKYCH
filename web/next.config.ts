import type { NextConfig } from "next";

// Build-time guard against the production-eo.kych.net symptom where
// every article detail page (and most other SSR fetches) silently fell
// back to `http://localhost:8000` from the EdgeOne edge runtime, never
// reached the backend, and rendered the catch-block "文章不存在" page
// for every article. Before this check the build would succeed, the
// operator would only find out from user reports, and the SSR log
// would show bare ECONNREFUSED with no hint about the actual fix
// (set NEXT_PUBLIC_API_BASE_URL in the EdgeOne console).
//
// Skip the check when:
//   - NODE_ENV !== "production" (dev falls back to localhost by design)
//   - STANDALONE === "1" (the docker-compose fallback — runs on the
//     same host as the backend, so `backend:8000` resolves inside
//     the docker network)
const isProd = process.env.NODE_ENV === "production";
const isStandalone = process.env.STANDALONE === "1";
if (isProd && !isStandalone && !process.env.NEXT_PUBLIC_API_BASE_URL) {
  throw new Error(
    "NEXT_PUBLIC_API_BASE_URL is required for production builds " +
    "(EdgeOne Makers). The browser bundle uses this env to point " +
    "cross-origin /api fetches at the Go backend. Without it, every " +
    "article detail page will render as \"文章不存在\" even though " +
    "the article exists — see docs/deployment.md §3.2."
  );
}

// CSP origin derivation. The backend lives on a different origin
// (https://api.ywda.net) from the frontends (cf.ywda.net / eo.ywda.net),
// so the browser makes cross-origin fetches to it for API calls, plus
// <img> from /uploads/* and <link> theme CSS. CSP must therefore allow
// that origin in connect-src / img-src / style-src / media-src, otherwise
// enforcing CSP would silently break every article page. In dev the env
// is unset (same-origin via next.config rewrites), so apiOrigin is "".
const apiBase = process.env.NEXT_PUBLIC_API_BASE_URL || "";
let apiOrigin = "";
try {
  if (apiBase) apiOrigin = new URL(apiBase).origin;
} catch {
  // Malformed env — leave empty; build guard above should have caught
  // production, but be defensive rather than crash the build here.
}

const cspHeader = [
  "default-src 'self'",
  // No per-request nonce: a nonce forced layout.tsx to call headers(),
  // which opted every route into dynamic rendering and disabled Next.js
  // Full Route Cache (so neither EdgeOne nor Cloudflare Workers could
  // cache SSR HTML at the edge). 'unsafe-inline' is the cache-friendly
  // trade-off; server-side sanitisation (bluemonday / DOMPurify / goldmark)
  // remains the primary XSS defence.
  // React dev mode + react-refresh + webpack-hmr all use eval() to
  // reconstruct callstacks / hot-swap modules — without 'unsafe-eval'
  // they fall back to thrown errors like "eval() is not supported",
  // filling the dev console with misleading red herrings. Production
  // never executes eval (React prod build strips it), so the keyword
  // is harmless there too — we keep it on for all envs to avoid a
  // dev ↔ prod CSP drift masking other CSP bugs.
  "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
  "style-src 'self' 'unsafe-inline'" + (apiOrigin ? " " + apiOrigin : ""),
  // Article HTML may embed external images (user content), so allow any
  // https image rather than just self/api origin.
  // 'self' is required so images uploaded to the same dev / prod origin
  // (e.g. /uploads/* served by Gin.Static in production rewrites, by
  // next rewrites in dev) survive the policy. Without 'self' the CSP
  // happily allows `https:` but rejects every same-origin upload because
  // it doesn't know the dev origin.
  "img-src 'self' https: data: blob:",
  "font-src 'self' data:",
  "connect-src 'self'" + (apiOrigin ? " " + apiOrigin : "") + " ws: wss:",
  "media-src 'self'" + (apiOrigin ? " " + apiOrigin : "") + " data: blob:",
  // Wikidot/HTML articles can embed YouTube etc. via <iframe>.
  "frame-src https:",
  "frame-ancestors 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join("; ");

const nextConfig: NextConfig = {
  // EdgeOne Makers (the current prod target) auto-detects Next.js and
  // builds with its own adapter — leave output un-set so its build picks
  // the right mode itself. Setting `output: "standalone"` here would
  // change the build shape and could conflict with EdgeOne's adapter.
  //
  // The docker-compose fallback still prefers standalone, so we keep a
  // STANDALONE=1 opt-in that only the VM-style Node-server path uses.
  // (EdgeOne's build pipeline doesn't set this env, so it's invisible
  // to that path.)
  ...(process.env.STANDALONE === "1" ? { output: "standalone" as const } : {}),

  // Static security headers (incl. CSP). Set via next.config rather than
  // middleware so they are baked into cached/ISR responses at the edge
  // (middleware may not run on platform-served cache hits). Removing the
  // old middleware.ts also removes the per-request nonce generation that
  // forced dynamic rendering.
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Content-Security-Policy", value: cspHeader },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
        ],
      },
    ];
  },

  // Proxy /api/*, /uploads/*, and /avatars/* to the Go backend in development.
  // /uploads and /avatars are served by Gin.Static in main.go; without these
  // rewrites Next.js dev would 404 any link that uses a path-relative URL
  //
  // DEV ONLY: Next.js 16 blocks any Host header other than `localhost` by
  // default, which would 502 any non-loopback or 127.0.0.1 access (LAN
  // phone, second browser, curl). Listing the common dev hosts lets them
  // serve the dev page — see
  // https://nextjs.org/docs/app/api-reference/config/next-config-js/allowedDevOrigins
  allowedDevOrigins: [
    "localhost",
    "127.0.0.1",
    "192.168.0.3",
    "192.168.0.9",
  ],
  // (e.g. the file-manager "open" link). Production does NOT run next dev /
  // next start behind EdgeOne — the standalone server.js is hit directly
  // by the origin pull, and cross-origin data goes through CORS +
  // PUBLIC_URL on the Go side, so these rewrites are dev-only.
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: "http://localhost:8000/api/:path*",
      },
      {
        source: "/uploads/:path*",
        destination: "http://localhost:8000/uploads/:path*",
      },
      {
        source: "/avatars/:path*",
        destination: "http://localhost:8000/avatars/:path*",
      },
    ];
  },
  // Fix Turbopack workspace root detection.
  turbopack: {
    root: process.cwd(),
  },
};

export default nextConfig;

// Cloudflare OpenNext dev integration: enables local bindings during `next dev`.
// EdgeOne and Docker standalone deployments ignore this (no-op when the package
// is not installed or when running in production builds).
try {
   
  const { initOpenNextCloudflareForDev } = require("@opennextjs/cloudflare");
  initOpenNextCloudflareForDev();
} catch {
  // @opennextjs/cloudflare is an optional devDependency; if not installed,
  // local development works without Cloudflare bindings (which is fine for
  // EdgeOne / Docker targets).
}
