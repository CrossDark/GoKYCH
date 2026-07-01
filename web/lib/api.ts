// Base URL of the Go backend API. Now that the frontend deployment target
// split from the backend (EdgeOne Makers edge runtime for Next.js, separate
// VM for the Go API), the browser-side fetch can't rely on same-origin
// relative paths anymore — it must hit `https://api.<host>` directly,
// which is a cross-origin request handled by the backend CORS middleware.
//
// `NEXT_PUBLIC_*` is required so the value is inlined into the client
// bundle at build time (bare `process.env.API_BASE_URL` is invisible to
// browser code). SSR (running on EdgeOne's edge function) sees the env at
// runtime, so reads it directly with the older `API_BASE_URL` as a fallback
// for legacy deployments that only set that name.
const BASE =
  process.env.NEXT_PUBLIC_API_BASE_URL ||
  (typeof window === "undefined" ? process.env.API_BASE_URL || "http://localhost:8000" : "");

// Per-request timeout for SSR fetches, in milliseconds. Without a timeout,
// a slow or unreachable backend can tie up an EdgeOne edge function for
// Node's default socket timeout (typically 2 minutes), exhausting the
// function concurrency pool and causing cascading 504s at the CDN layer.
const SSR_FETCH_TIMEOUT_MS = 8000;

// ISR revalidation interval (seconds). Article content / lists / site
// config are revalidated in the background after this many seconds; the
// CDN serves a stale copy while Next.js regenerates, so users never
// wait on a cold SSR. Comments/rating/user-* endpoints are NOT cached
// (user-specific, must always be fresh) — see the per-function opts below.
const DEFAULT_REVALIDATE = 60;

// isSSR is true when running on the server (Edge Function / Node).
const isSSR = typeof window === "undefined";

/**
 * Build the absolute URL for a backend endpoint.
 *
 * In production the Next.js frontend runs on EdgeOne Makers (a separate
 * origin from the Go backend), so any fetch that the BROWSER makes
 * against `/api/...` must be rewritten to `https://<api host>/api/...`
 * — otherwise it would hit EdgeOne's edge runtime, which doesn't proxy
 * arbitrary paths to the backend.
 *
 * On the SSR side the BASE already resolves to a usable URL
 * (`API_BASE_URL` or `http://localhost:8000`), so the same helper works
 * for server components / route handlers too.
 *
 * When no env is set (local `next dev`), BASE is empty on the browser
 * and the relative path is preserved — `next.config.ts` rewrites it to
 * `http://localhost:8000` for the dev server.
 */
export function apiUrl(path: string): string {
  if (!path.startsWith("/")) path = "/" + path;
  return `${BASE}${path}`;
}

/**
 * Cross-origin fetch wrapper for the admin SPA.
 *
 * Bare `fetch()` defaults to `credentials: "same-origin"`, which silently
 * drops the api.kych.net session cookie when the SPA is hosted on a
 * different origin (eo.kych.net). Every /api/* call from the browser
 * MUST go through this so the cookie is attached — otherwise every
 * owner-gated endpoint 401s with no obvious cause (the request leaves
 * the browser without a Cookie header, the server sees no session, and
 * `requireOwner` aborts with 401).
 *
 * On the server this also injects an AbortController timeout so a hung
 * backend can't pin an edge function indefinitely, and attaches
 * `next: { revalidate }` to make GET responses eligible for Next's data
 * cache + ISR.
 */
export function apiFetch(
  input: string,
  init?: RequestInit & { next?: { revalidate?: number; tags?: string[] }; timeoutMs?: number }
): Promise<Response> {
  const { timeoutMs, next: nextOpts, ...rest } = init || {};

  if (isSSR) {
    // Build an AbortController that fires after timeoutMs so a slow
    // backend doesn't hang the edge function. If the caller already
    // supplied a signal, we chain both signals.
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs || SSR_FETCH_TIMEOUT_MS);
    let signal: AbortSignal = controller.signal;
    if (rest.signal) {
      // Chain the user-supplied signal too.
      const userSignal = rest.signal;
      if (userSignal.aborted) {
        controller.abort();
      } else {
        userSignal.addEventListener("abort", () => controller.abort(), { once: true });
      }
    }

    // Next.js 15 extends fetch with a `next` option for data caching.
    // Cast through unknown because the TS lib dom types don't know
    // about Next's extension, but the runtime does.
    const nextCfg: any = {};
    if (nextOpts?.revalidate !== undefined) {
      nextCfg.revalidate = nextOpts.revalidate;
    } else if (!rest.method || rest.method === "GET") {
      // Default GET caching (ISR). Mutating methods (POST/PUT/DELETE)
      // go through uncached.
      nextCfg.revalidate = DEFAULT_REVALIDATE;
    }
    if (nextOpts?.tags) nextCfg.tags = nextOpts.tags;

    return fetch(input, {
      ...rest,
      signal,
      next: nextCfg,
    } as any).finally(() => clearTimeout(timer)) as Promise<Response>;
  }

  // Browser: attach credentials for cross-origin cookie flow.
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

async function request<T>(
  path: string,
  options?: RequestInit & { next?: { revalidate?: number; tags?: string[] }; timeoutMs?: number; anon?: boolean }
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options?.headers as Record<string, string>),
  };

  // Forward session cookie on server-side so Go API can identify the user.
  // Skip for public/anon requests (article content, lists, site config,
  // labels) — these must NOT carry the session cookie because they are
  // cached by ISR, and serving user A's personalised response (with
  // can_edit=true, their own rating, etc.) from cache to user B would
  // leak identity data. Client-side calls (water) always use
  // credentials:include so personalisation happens after hydration
  // without poisoning the shared HTML cache.
  if (isSSR && !options?.anon) {
    const cookieStr = await getServerCookies();
    if (cookieStr) headers["Cookie"] = cookieStr;
  }

  const res = await apiFetch(`${BASE}/api${path}`, {
    ...options,
    headers,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || "请求失败");
  }
  return res.json();
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// ── React.cache-based SSR deduplication ────────────────────────────
//
// Wrap every read-only data fetcher in React.cache so that calling the
// same function with the same arguments multiple times during a single
// render (e.g. generateMetadata + the page component, or nested server
// components) de-duplicates to one real network request. This is
// critical on EdgeOne where each SSR hop to api.kych.net costs
// hundreds of ms in TLS handshake + cross-region latency.
//
// React.cache only deduplicates within a single request tree, so there
// is no cross-user leakage risk. It's a no-op on the client (React
// doesn't invoke it outside render), where we handle deduplication
// separately via in-flight Promise tracking below.

import { cache } from "react";

// ── Client-side in-flight de-duplication ───────────────────────────
//
// On the client, multiple components (Header, Footer, ArticleView,
// RatingWidget, CommentSection) all call getMe()/getCsrf()/getSite()
// on mount. Without dedup that's 4+ concurrent identical requests to
// the backend for the same data. We track in-flight Promises at module
// scope so concurrent callers await the same network round-trip.
type InFlight<T> = Promise<T>;
const inFlight = new Map<string, InFlight<any>>();

function dedupClient<T>(key: string, fn: () => Promise<T>): Promise<T> {
  if (isSSR) return fn(); // SSR uses React.cache, not this map
  const existing = inFlight.get(key);
  if (existing) return existing as Promise<T>;
  const p = fn().finally(() => inFlight.delete(key));
  inFlight.set(key, p);
  return p;
}

// ── Auth ──────────────────────────────────────────────────────────

// getMe is called from Header, ArticleView, RatingWidget, CommentSection.
// On the client we dedup concurrent mount-time calls; on the server we
// use React.cache. We intentionally do NOT cache across requests (no
// revalidate) because the response depends on the session cookie.
const _getMeSSR = cache(() =>
  request<{ user: import("./types").User | null }>("/auth/me", {
    next: { revalidate: 0 }, // never cache auth
  })
);
export function getMe() {
  if (isSSR) return _getMeSSR();
  return dedupClient("me", () =>
    request<{ user: import("./types").User | null }>("/auth/me")
  );
}

// getCsrf is called from every interactive component; same dedup story.
const _getCsrfSSR = cache(() =>
  request<import("./types").CaptchaResponse>("/auth/csrf", {
    next: { revalidate: 0 },
  })
);
export function getCsrf() {
  if (isSSR) return _getCsrfSSR();
  return dedupClient("csrf", () =>
    request<import("./types").CaptchaResponse>("/auth/csrf")
  );
}

export function login(body: {
  username: string;
  password: string;
  captcha: string;
  csrf_token: string;
}) {
  return request<import("./types").LoginResponse>("/auth/login", {
    method: "POST",
    headers: { "X-CSRF-Token": body.csrf_token },
    body: JSON.stringify(body),
  });
}

export function logout(csrfToken: string) {
  return request<{ status: string }>("/auth/logout", {
    method: "POST",
    headers: { "X-CSRF-Token": csrfToken },
  });
}

// ── Articles ──────────────────────────────────────────────────────
// authorId: when set, server filters to articles authored by this user (used
// by the "我的文章" view for regular users on /admin/articles).
export function listArticles(
  type?: string,
  page = 1,
  authorId?: number
) {
  const q = new URLSearchParams();
  if (type) q.set("type", type);
  if (authorId) q.set("author_id", String(authorId));
  q.set("page", String(page));
  // Article lists are public, ISR-cacheable for 60s. Anon = no cookie
  // forwarding so the cached HTML is identical for all visitors.
  const opts: any = { anon: true };
  if (authorId) {
    // "My articles" is user-specific; don't cache, forward cookie.
    opts.anon = false;
    opts.next = { revalidate: 0 };
  }
  return request<import("./types").ArticleListResult>(
    `/articles?${q.toString()}`, opts
  );
}

// getArticle is the hottest SSR call (generateMetadata + page component
// both call it for every article view). Wrap in React.cache so the two
// callsites share one network round-trip. Anon = ISR-safe.
const _getArticleSSR = cache((type: string, slug: string) =>
  request<import("./types").ArticleDetail>(`/articles/${type}/${slug}`, { anon: true })
);
export function getArticle(type: string, slug: string) {
  if (isSSR) return _getArticleSSR(type, slug);
  return request<import("./types").ArticleDetail>(`/articles/${type}/${slug}`);
}

export function createArticle(
  type: string,
  csrf: string,
  body: { slug: string; title: string; content: string; tags?: string[] }
) {
  return request<import("./types").Article>(
    `/articles?type=${type}`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function updateArticle(
  type: string,
  slug: string,
  csrf: string,
  body: { title: string; content: string; tags?: string[] }
) {
  return request<import("./types").Article>(
    `/articles/${type}/${slug}`,
    {
      method: "PUT",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function deleteArticle(type: string, slug: string, csrf: string) {
  return request<{ status: string }>(
    `/articles/${type}/${slug}`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

// ── Comments ──────────────────────────────────────────────────────
export function addComment(
  type: string, slug: string, csrf: string,
  body: { content: string; author_name?: string }
) {
  return request<import("./types").Comment>(
    `/articles/${type}/${slug}/comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

// ── Line Comments ─────────────────────────────────────────────────
export function getLineComments(type: string, slug: string) {
  return request<{ counts: Record<number, number>; comments: (import("./types").Comment & { line_number: number })[] }>(
    `/articles/${type}/${slug}/line-comments`
  );
}

export function addLineComment(
  type: string, slug: string, csrf: string,
  body: { line_number: number; content: string; author_name?: string }
) {
  return request<import("./types").Comment>(
    `/articles/${type}/${slug}/line-comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

// ── Rating ────────────────────────────────────────────────────────
export function getRating(type: string, slug: string) {
  // Rating includes user_score which is session-dependent; don't cache
  // on the server (different users see different values).
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    isSSR ? { next: { revalidate: 0 } } : undefined
  );
}

export function setRating(
  type: string, slug: string, csrf: string, score: number
) {
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify({ score }),
    }
  );
}

export function undoRating(type: string, slug: string, csrf: string) {
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

export function getRatingDetails(type: string, slug: string) {
  return request<{ ratings: import("./types").RatingDetail[] }>(
    `/articles/${type}/${slug}/ratings`
  );
}

// ── Home ──────────────────────────────────────────────────────────
// getHome is called once per homepage render. React-cache + ISR means
// it's cheap; cache it at the request level too. Anon = ISR-safe.
const _getHomeSSR = cache(() => request<import("./types").HomeData>("/home", { anon: true }));
export function getHome() {
  if (isSSR) return _getHomeSSR();
  return request<import("./types").HomeData>("/home");
}

// ── Site config ──────────────────────────────────────────────────
//
// One-shot read of title/subtitle/theme/ICP/subsite_links, used by the
// global Header and LayoutWrapper footer. Public endpoint, no auth.
// This is called from BOTH Header and Footer (client components), and
// we dedup client-side via the inflight map above. On the server the
// header/footer are client components so they don't run during SSR,
// but we still cache for any server-side callers. Anon = ISR-safe.
const _getSiteSSR = cache(() => request<import("./types").SiteConfig>("/site", { anon: true }));
export function getSite() {
  if (isSSR) return _getSiteSSR();
  return dedupClient("site", () => request<import("./types").SiteConfig>("/site"));
}

// Public theme list — names + meta + has_css. Used by the admin settings
// page to populate the theme dropdown. Theme CSS itself is fetched
// directly from /api/themes/:name.css (text/css, no JSON wrapping).
export function listThemes() {
  return request<import("./types").Theme[]>("/themes", { anon: true });
}

// ── Labels ────────────────────────────────────────────────────────
const _listLabelsSSR = cache(() => request<import("./types").TagWithCount[]>("/labels", { anon: true }));
export function listLabels() {
  if (isSSR) return _listLabelsSSR();
  return dedupClient("labels", () => request<import("./types").TagWithCount[]>("/labels"));
}

export function getLabelArticles(tag: string, page = 1) {
  return request<import("./types").ArticleListResult>(
    `/labels/${encodeURIComponent(tag)}?page=${page}`,
    { anon: true }
  );
}

// ── Search ────────────────────────────────────────────────────────
export function search(q: string, page = 1) {
  // Search results are user-query-specific; use a shorter revalidate
  // (30s) so repeated queries are fast but fresh edits surface quickly.
  return request<import("./types").ArticleListResult>(
    `/search?q=${encodeURIComponent(q)}&page=${page}`,
    isSSR ? { next: { revalidate: 30 }, anon: true } : undefined
  );
}

// ── Admin: Users ──────────────────────────────────────────────────
export function listUsers(csrf: string) {
  return request<import("./types").User[]>("/admin/users", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createUser(csrf: string, body: { username: string; password: string; nickname?: string; role?: string }) {
  return request<import("./types").User>("/admin/users", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function updateUserRole(csrf: string, username: string, role: string) {
  return request<{ status: string }>(`/admin/users/${username}/role`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ role }),
  });
}

export function deleteUser(csrf: string, username: string) {
  return request<{ status: string }>(`/admin/users/${username}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Notifications ──────────────────────────────────────────
export function listNotifications(csrf: string) {
  return request<import("./types").Notification[]>("/admin/notifications", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createNotification(csrf: string, body: { title: string; content: string; is_important?: boolean }) {
  return request<import("./types").Notification>("/admin/notifications", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function updateNotification(csrf: string, id: number, body: { title?: string; content?: string; is_important?: boolean; is_active?: boolean }) {
  return request<import("./types").Notification>(`/admin/notifications/${id}`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function deleteNotification(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/notifications/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Settings ───────────────────────────────────────────────
export function getSettings(csrf: string) {
  return request<Record<string, any>>("/admin/settings", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function updateSettings(csrf: string, settings: Record<string, any>) {
  return request<{ status: string }>("/admin/settings", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(settings),
  });
}

// ── Admin: Homepage ───────────────────────────────────────────────
export function getAdminHome(csrf: string) {
  return request<{
    subsite_links: import("./types").SubsiteLink[];
    featured_articles: import("./types").FeaturedArticle[];
  }>("/admin/home", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function addSubsiteLink(csrf: string, body: { name: string; url: string; description?: string; sort_order?: number }) {
  return request<{ status: string }>("/admin/home/links", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function deleteSubsiteLink(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/home/links/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function addFeatured(csrf: string, articleId: number) {
  return request<{ status: string }>("/admin/home/featured", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ article_id: articleId }),
  });
}

export function deleteFeatured(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/home/featured/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Profile ────────────────────────────────────────────────
export function getProfile(csrf: string) {
  return request<import("./types").User>("/admin/profile", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function updateProfile(csrf: string, body: {
  nickname?: string;
  bio?: string;
  avatar?: string;
  social_email?: string;
  social_github?: string;
  social_qq?: string;
}) {
  return request<import("./types").User>("/admin/profile", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

// Self-service password change. Any logged-in user can change their own
// password; the handler verifies the old one before accepting the new.
export function changeMyPassword(csrf: string, body: { old_password: string; new_password: string }) {
  return request<{ status: string; message?: string }>("/admin/profile/password", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

// ── Admin: Tags (full CRUD) ─────────────────────────────────────────
// Used by /admin/tags — the public /api/labels endpoint only returns tags
// that have at least one article, so admins need a separate route to manage
// empty tags too.
export function listAdminTags(csrf: string) {
  return request<import("./types").AdminTag[]>("/admin/tags", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createTag(csrf: string, name: string) {
  return request<{ id: number; status: string; existed?: boolean }>("/admin/tags", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ name }),
  });
}

export function renameTag(csrf: string, id: number, name: string) {
  return request<{ status: string }>(`/admin/tags/${id}`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ name }),
  });
}

export function deleteAdminTag(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/tags/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Files (upload/delete) ────────────────────────────────────
// listAdminFiles is already declared above; uploadFile/deleteFile cover the
// write paths.
export function uploadFile(csrf: string, file: File) {
  const fd = new FormData();
  fd.append("file", file);
  // When sending FormData the browser sets its own multipart Content-Type
  // with boundary — passing "Content-Type: application/json" from `request`
  // would break the upload, so we build the fetch manually.
  const headers: Record<string, string> = { "X-CSRF-Token": csrf };
  if (isSSR) {
    // Server-side upload isn't expected in this UI, but keep the path working
    // if it ever is.
    return import("next/headers").then(async ({ cookies }) => {
      const jar = await cookies();
      const cookieStr = jar.getAll().map((c) => `${c.name}=${c.value}`).join("; ");
      headers["Cookie"] = cookieStr;
      const res = await apiFetch(apiUrl("/api/admin/files"), {
        method: "POST",
        headers,
        body: fd,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ApiError(res.status, body.error || "上传失败");
      }
      return res.json() as Promise<{ status: string; filename: string; url: string; deduped?: boolean }>;
    });
  }
  return apiFetch(apiUrl("/api/admin/files"), {
    method: "POST",
    headers,
    body: fd,
  }).then(async (res) => {
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new ApiError(res.status, body.error || "上传失败");
    }
    return res.json() as Promise<{ status: string; filename: string; url: string; deduped?: boolean }>;
  });
}

export function deleteAdminFile(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/files/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function listAdminFiles(csrf: string) {
  return request<import("./types").AdminFile[]>("/admin/files", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}
