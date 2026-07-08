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
  return dedupClient("site", () => request<SiteConfig>("/site"));
}