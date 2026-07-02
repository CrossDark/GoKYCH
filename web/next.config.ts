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
    "the article exists — see docs/deployment.md §5."
  );
}

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

  // Proxy /api/*, /uploads/*, and /avatars/* to the Go backend in development.
  // /uploads and /avatars are served by Gin.Static in main.go; without these
  // rewrites Next.js dev would 404 any link that uses a path-relative URL
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

import('@opennextjs/cloudflare').then(m => m.initOpenNextCloudflareForDev());
