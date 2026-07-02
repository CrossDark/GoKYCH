import { cache } from "react";

const BASE =
  process.env.NEXT_PUBLIC_API_BASE_URL ||
  (typeof window === "undefined" ? process.env.API_BASE_URL || "http://localhost:8000" : "");

const SSR_FETCH_TIMEOUT_MS = 8000;

const DEFAULT_REVALIDATE = 60;

export const isSSR = typeof window === "undefined";

export function apiUrl(path: string): string {
  if (!path.startsWith("/")) path = "/" + path;
  return `${BASE}${path}`;
}

export function apiFetch(
  input: string,
  init?: RequestInit & { next?: { revalidate?: number; tags?: string[] }; timeoutMs?: number }
): Promise<Response> {
  const { timeoutMs, next: nextOpts, ...rest } = init || {};

  if (isSSR) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs || SSR_FETCH_TIMEOUT_MS);
    let signal: AbortSignal = controller.signal;
    if (rest.signal) {
      const userSignal = rest.signal;
      if (userSignal.aborted) {
        controller.abort();
      } else {
        userSignal.addEventListener("abort", () => controller.abort(), { once: true });
      }
    }

    const nextCfg: any = {};
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
    } as any).finally(() => clearTimeout(timer)) as Promise<Response>;
  }

  return fetch(input, {
    credentials: "include",
    ...rest,
  });
}

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

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export type RequestOptions = RequestInit & {
  next?: { revalidate?: number; tags?: string[] };
  timeoutMs?: number;
  anon?: boolean;
  raw?: boolean;
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