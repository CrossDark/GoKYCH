import { cache } from "react";

// BASE resolves the API origin for every fetch/href in the app:
//   1. NEXT_PUBLIC_API_BASE_URL — production cross-origin absolute URL
//      (required by EdgeOne Makers / Cloudflare Workers builds where
//      SSR and browser both hit the Go backend on a different origin)
//   2. API_BASE_URL — SSR-only fallback (Node server-side env)
//   3. "http://localhost:8000" — SSR dev fallback (next dev rewrites
//      proxy /api/* to the Go backend on :8000; see next.config.ts)
//   4. "" (empty string) — browser-side dev; fetches use relative
//      paths that next dev rewrites handle
const BASE =
  process.env.NEXT_PUBLIC_API_BASE_URL ||
  (typeof window === "undefined" ? process.env.API_BASE_URL || "http://localhost:8000" : "");

// SSR_FETCH_TIMEOUT_MS bounds server-side fetches so a hung backend
// can't tie up an EdgeOne/Cloudflare edge worker indefinitely.
const SSR_FETCH_TIMEOUT_MS = 8000;

// DEFAULT_REVALIDATE is the Next.js ISR revalidate window for GET
// requests that don't specify their own. 3600s (1h) gives a high edge-cache
// hit ratio (space-for-time); on-demand revalidation (webhook) keeps cached
// content fresh on edits, so a long window is safe.
const DEFAULT_REVALIDATE = 3600;

export const isSSR = typeof window === "undefined";

// apiUrl prepends BASE to a path, ensuring leading slash.
// Used for both fetch() calls and <a href>/<img src>/download links.
export function apiUrl(path: string): string {
  if (!path.startsWith("/")) path = "/" + path;
  return `${BASE}${path}`;
}

// apiFetch wraps fetch() with two runtime-specific behaviours:
//   - SSR: attaches an AbortController timeout (SSR_FETCH_TIMEOUT_MS),
//     forwards Next.js revalidate/tags options for ISR, and merges a
//     caller-supplied signal with the timeout.
//   - Browser: always sets credentials: "include" so session cookies
//     are sent cross-origin (CORS on the Go side whitelists origins).
export function apiFetch(
  input: string,
  init?: RequestInit & { next?: { revalidate?: number; tags?: string[] }; timeoutMs?: number }
): Promise<Response> {
  const { timeoutMs, next: nextOpts, ...rest } = init || {};

  if (isSSR) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs || SSR_FETCH_TIMEOUT_MS);
    const signal: AbortSignal = controller.signal;
    if (rest.signal) {
      const userSignal = rest.signal;
      if (userSignal.aborted) {
        controller.abort();
      } else {
        userSignal.addEventListener("abort", () => controller.abort(), { once: true });
      }
    }

    const nextCfg: { revalidate?: number; tags?: string[] } = {};
    if (nextOpts?.revalidate !== undefined) {
      nextCfg.revalidate = nextOpts.revalidate;
    } else if (!rest.method || rest.method === "GET") {
      nextCfg.revalidate = DEFAULT_REVALIDATE;
    }
    if (nextOpts?.tags) nextCfg.tags = nextOpts.tags;

    return fetch(input, {
      ...rest,
      signal,
      next: nextCfg,
    }).finally(() => clearTimeout(timer)) as Promise<Response>;
  }

  return fetch(input, {
    credentials: "include",
    ...rest,
  });
}

// getServerCookies reads cookies from next/headers during SSR so we
// can forward the session cookie to the Go backend in cross-origin
// setups (EdgeOne/Cloudflare). Without this, SSR fetches would appear
// anonymous even when the user is logged in — the rating slider would
// show "0.0 (你的评分 --)" instead of the user's actual vote.
// Returns "" when called in the browser or when next/headers is
// unavailable (e.g. during static generation).
async function getServerCookies(): Promise<string> {
  if (!isSSR) return "";
  try {
    const { cookies } = await import("next/headers");
    const jar = await cookies();
    return jar.getAll().map((c) => `${c.name}=${c.value}`).join("; ");
  } catch {
    return "";
  }
}

// ApiError is thrown by request() when the backend returns a non-2xx
// response. The `status` field carries the HTTP status code so callers
// can distinguish 401 (login expired) from 403 (forbidden) from 500.
export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export type RequestOptions = RequestInit & {
  next?: { revalidate?: number; tags?: string[] };
  timeoutMs?: number;
  anon?: boolean; // skip forwarding SSR cookies (for public endpoints like /api/site)
  raw?: boolean;  // return the raw Response instead of parsing JSON
};

export function request(path: string, options?: RequestOptions & { raw: true }): Promise<Response>;
export function request<T>(path: string, options?: RequestOptions & { raw?: false }): Promise<T>;
export async function request<T>(path: string, options?: RequestOptions): Promise<T | Response> {
  const { raw, anon, ...rest } = options || {};

  const isFormData = rest.body instanceof FormData;

  const headers: Record<string, string> = {};
  if (!isFormData) {
    headers["Content-Type"] = "application/json";
  }
  if (rest.headers) {
    Object.assign(headers, rest.headers as Record<string, string>);
  }

  if (isSSR && !anon) {
    const cookieStr = await getServerCookies();
    if (cookieStr) headers["Cookie"] = cookieStr;
  }

  const res = await apiFetch(`${BASE}/api${path}`, {
    ...rest,
    headers,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || "请求失败");
  }

  if (raw) {
    return res;
  }

  return res.json();
}

// dedupClient deduplicates concurrent in-flight requests for the same
// key on the client side (e.g. multiple components mounting at once
// and all calling getMe()). SSR uses React's `cache()` instead (see
// imports above). Server-side dedup is a no-op because React cache
// already handles request-scoped dedup.
type InFlight<T> = Promise<T>;
const inFlight = new Map<string, InFlight<any>>();

export function dedupClient<T>(key: string, fn: () => Promise<T>): Promise<T> {
  if (isSSR) return fn();
  const existing = inFlight.get(key);
  if (existing) return existing as Promise<T>;
  const p = fn().finally(() => inFlight.delete(key));
  inFlight.set(key, p);
  return p;
}

export { cache };
