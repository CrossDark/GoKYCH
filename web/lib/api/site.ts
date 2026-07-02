import { request, cache, dedupClient, isSSR } from "./client";
import type { SiteConfig } from "@/lib/types";

const _getSiteSSR = cache(() => request<SiteConfig>("/site", { anon: true }));

export function getSite() {
  if (isSSR) return _getSiteSSR();
  return dedupClient("site", () => request<SiteConfig>("/site"));
}