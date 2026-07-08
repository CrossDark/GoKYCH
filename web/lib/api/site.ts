import { request, cache, dedupClient, isSSR } from "./client";
import type { SiteConfig } from "@/lib/types";

// Cache tag "site" — fired by the Go backend's revalidateFrontend()
// after settings mutations (theme activate, site title/logo change,
// features toggle). Without this tag the 3600s ISR window wins and
// admin settings changes don't show up for up to an hour even when the
// next.config revalidate webhook is invoked. See internal/api/revalidate.go.
const _getSiteSSR = cache(() => request<SiteConfig>("/site", {
  anon: true,
  next: { revalidate: 3600, tags: ["site"] },
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