import { request, cache, dedupClient, isSSR } from "./client";
import type { SiteConfig } from "@/lib/types";

// `revalidate: 0` (not 3600) because the /api/site response carries
// `Cache-Control: public, max-age=3600` from internal/api/site.go, and
// admin mutations (theme activate, site title/logo change, features
// toggle) want to surface immediately on refresh. The cache tag
// "site" is still attached so the Go backend's revalidateFrontend()
// can selectively bust the cache on settings writes — but in this
// app the per-fetch dedup (`React.cache` on the server, `dedupClient`
// on the client) plus a single fresh-fetch-per-render is the right
// trade-off: settings.yml load is cheap (YAML unmarshal of a few KB),
// and admin UX demands a refresh-then-see-new-theme flow.
//
// 3600s here would also pin dev mode to stale values via Next.js 16's
// fetch cache — `revalidate: 0` lets the dev server re-fetch on
// every request so a quick HMR doesn't leave you staring at the
// previous activation for an hour.
const _getSiteSSR = cache(() => request<SiteConfig>("/site", {
  anon: true,
  next: { revalidate: 0, tags: ["site"] },
}));

export function getSite() {
  if (isSSR) return _getSiteSSR();
  // No browser cache on the client side. The backend sets
  // Cache-Control: public, max-age=3600 on /api/site (see
  // internal/api/site.go) so the edge can serve repeat visitors
  // without hitting the origin — but that same header would also
  // pin the browser's disk cache for an hour, meaning an admin
  // who just activated a new theme (settings mutation) would see
  // the old style_theme on refresh until the cache expires.
  // Next.js ISR (the SSR path above) handles the cross-user cache;
  // here on the client we want freshness over cache reuse.
  return dedupClient("site", () =>
    request<SiteConfig>("/site", { cache: "no-store" })
  );
}