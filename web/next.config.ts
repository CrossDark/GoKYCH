import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Proxy /api/*, /uploads/*, and /avatars/* to the Go backend in development.
  // /uploads and /avatars are served by Gin.Static in main.go; without these
  // rewrites Next.js dev would 404 any link that uses a path-relative URL
  // (e.g. the file-manager "open" link).
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
