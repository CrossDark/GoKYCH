import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Proxy /api/* to the Go backend in development.
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: "http://localhost:8000/api/:path*",
      },
    ];
  },
  // Fix Turbopack workspace root detection.
  turbopack: {
    root: process.cwd(),
  },
};

export default nextConfig;
