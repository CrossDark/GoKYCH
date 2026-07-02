// Next.js Middleware — runs on every non-static request before the
// route handler. Generates a per-request CSP nonce and sets security
// headers. Compatible with both the Node.js runtime and Cloudflare
// Edge Runtime (uses Web Crypto API, not Node crypto).
//
// The CSP nonce is passed via the X-Nonce request header; the server
// component layout (app/layout.tsx) reads it and injects it into
// <script> tags so inline scripts produced by Next.js are allowed
// while blocking third-party script injection.
//
// Static assets (_next/static, _next/image, favicon.ico, uploads,
// avatars) are excluded from the matcher — they don't need CSP headers
// and skipping them lets the CDN cache them effectively.

import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// generateNonce produces a 16-byte random nonce encoded as base64.
// Uses crypto.getRandomValues (Web Crypto API) which is available in
// both Node.js 19+ and Cloudflare Edge Runtime.
function generateNonce(): string {
  const buf = new Uint8Array(16);
  crypto.getRandomValues(buf);
  let binary = "";
  for (let i = 0; i < buf.length; i++) {
    binary += String.fromCharCode(buf[i]);
  }
  return btoa(binary);
}

export function middleware(request: NextRequest) {
  const nonce = generateNonce();

  // Content-Security-Policy:
  //   - default-src 'self': only load resources from same origin by default
  //   - script-src 'nonce-...': only allow scripts with the correct nonce
  //     (Next.js inline bootstrap scripts are rendered with the nonce)
  //   - style-src 'unsafe-inline': required for Next.js's dynamic style
  //     injection (styled-jsx / CSS-in-JS); nonce-based style enforcement
  //     breaks most Next.js apps as of 16.x
  //   - img-src: allow data: URIs (KaTeX fonts, emoji) and blob: (Mermaid)
  //   - connect-src: allow WebSocket (HMR in dev)
  //   - frame-ancestors 'none': prevent clickjacking
  //
  // Note: cross-origin API fetches to NEXT_PUBLIC_API_BASE_URL are NOT
  // blocked because they go to an absolute URL — CSP connect-src 'self'
  // only restricts same-origin fetches; cross-origin is governed by CORS
  // on the server side, not CSP.
  const cspHeader = [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}'`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self' ws: wss:",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join("; ");

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("X-Nonce", nonce);

  const response = NextResponse.next({
    request: {
      headers: requestHeaders,
    },
  });
  response.headers.set("Content-Security-Policy", cspHeader);
  response.headers.set("X-Content-Type-Options", "nosniff");
  response.headers.set("X-Frame-Options", "DENY");
  response.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");

  return response;
}

export const config = {
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico|uploads|avatars).*)"],
};
