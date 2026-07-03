import { getSite, listLabels, listThemes } from "@/lib/api";
import { AppProviders } from "./AppProviders";
import type { SiteConfig, TagWithCount, Theme } from "@/lib/types";

// AppData is the server-component shell that SSR-fetches the public,
// cacheable site-wide data (site config / tag list / theme list) once and
// hands it to the client <AppProviders> context. All three fetches are
// `anon` (no cookie forwarding → no cookies()/headers() dynamic API) and
// carry ISR revalidate, so they compose cleanly with Full Route Cache once
// the per-request CSP nonce is gone. Each is also individually React-cache
// deduped, so sharing a request with layout.generateMetadata's getSite()
// is free.
//
// Failures degrade gracefully: a null site falls back to the default title
// string ("跨越晨昏"), empty labels/themes just hide the sidebar/theme
// stylesheet — no page-level crash.
export async function AppData({ children }: { children: React.ReactNode }) {
  const [site, labels, themes] = await Promise.all([
    getSite().catch(() => null),
    listLabels().catch(() => [] as TagWithCount[]),
    listThemes().catch(() => [] as Theme[]),
  ]);
  return (
    <AppProviders
      site={site as SiteConfig | null}
      labels={labels as TagWithCount[]}
      themes={themes as Theme[]}
    >
      {children}
    </AppProviders>
  );
}
