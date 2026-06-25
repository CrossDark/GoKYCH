import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Standalone output is what the Node-server deployment (源站 + EdgeOne
  // 加速) needs: Next.js trims node_modules to only the deps actually
  // used and emits .next/standalone/server.js, so the runtime image / VM
  // ships a single self-contained dir instead of the full node_modules.
  // Dev / CI are unaffected — `next dev` and `next build` still work.
  // NOTE: this is NOT the Cloudflare "edge runtime" mode; if you ever go
  // back to a V8-isolate host (CF Pages + @cloudflare/next-on-pages), drop
  // this and use that adapter's requirements instead.
  output: "standalone",

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
