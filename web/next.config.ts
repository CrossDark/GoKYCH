import type { NextConfig } from "next";

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
