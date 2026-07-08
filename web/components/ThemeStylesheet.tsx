"use client";

import { Fragment } from "react";
import { useApp } from "./AppProviders";
import { apiUrl } from "@/lib/api";

/**
 * ThemeStylesheet — loads /api/themes/<style_theme>.css into the document
 * so the active theme's :root variables override the built-in defaults
 * from globals.css.
 *
 * Also loads theme-bundled assets (currently only the glass theme ships a
 * `static/effects/particles.js`; the file is fetched from
 * /api/themes/<name>/assets/... which is restricted to the theme's
 * static/ subdir on the backend). Themes that don't ship extra assets
 * pay no extra cost — the <script> tag is omitted.
 *
 * Previously this fired two client-side fetches (getSite + listThemes) on
 * every mount. It now reads both from the <AppData> SSR context, so the
 * <link> is computed synchronously and present in the SSR HTML — no flash,
 * no client fetch, and ISR-friendly (no dynamic API).
 *
 * The theme's updated_at timestamp is appended as ?v= for cache-busting
 * (same URL while unchanged → CDN/browser cache; new timestamp on edit →
 * cache miss).
 *
 * SSR / hydration note: we route the URLs through `apiUrl()` so the
 * browser hits the backend origin directly (api.kych.net / api.ywda.net)
 * instead of the frontend edge (eo.kych.net / cf.ywda.net). EdgeOne and
 * Cloudflare Workers have no Next.js API route for `/api/themes/*`
 * (`web/app/api/` doesn't exist — these endpoints are served by the Go
 * backend through nginx `/api/*` reverse proxy), so a relative
 * `/api/themes/css` request would hit the edge's default 504/500 fallback
 * and stall the page render for 30+ seconds. Absolute URLs skip the edge
 * entirely. `apiUrl()` returns different values between SSR
 * (http://localhost:8000 in dev) and client (`""` in dev), so we mark the
 * <link>/<script> tags with `suppressHydrationWarning` — same pattern
 * used for the typst PDF download link (see ArticleView.tsx).
 */
export function ThemeStylesheet() {
  const { site, themes } = useApp();
  const name = (site?.appearance as { style_theme?: string } | undefined)
    ?.style_theme;
  if (!name || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) return null;

  const theme = themes.find((t) => t.name === name);
  const cacheBust = theme?.updated_at
    ? `?v=${new Date(theme.updated_at).getTime()}`
    : "";
  // Absolute URL on the backend origin — bypass the frontend edge where
  // there's no API route handler for /api/themes/*.
  const cssUrl = `${apiUrl(`/api/themes/${encodeURIComponent(name)}`)}${cacheBust}`;

  // Theme-bundled JS assets. Add new entries here as more themes ship
  // their own scripts. Particles script is `defer` so it runs after CSS
  // is applied and after the body is parsed — it needs the body element
  // and the CSS variable to be live.
  const assets: { src: string; type: string }[] = [];
  if (name === "glass") {
    assets.push({ src: `${apiUrl(`/api/themes/glass/assets/effects/particles.js`)}${cacheBust}`, type: "module-shim" });
  }

  return (
    <Fragment>
      {/* suppressHydrationWarning: apiUrl() differs between SSR and client
       * in dev mode (localhost:8000 vs ""), producing a known mismatch in
       * the rendered href. Production bakes NEXT_PUBLIC_API_BASE_URL into
       * both sides so they agree — the warning only fires in dev. */}
      <link rel="stylesheet" href={cssUrl} data-theme-stylesheet="active" suppressHydrationWarning />
      {assets.map((a) => (
        <script
          key={a.src}
          defer
          src={a.src}
          data-theme-asset={name}
          suppressHydrationWarning
        />
      ))}
    </Fragment>
  );
}

