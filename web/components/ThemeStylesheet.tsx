"use client";

import { useApp } from "./AppProviders";

/**
 * ThemeStylesheet — loads /api/themes/<style_theme>.css into the document
 * so the active theme's :root variables override the built-in defaults
 * from globals.css.
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
 * SSR / hydration note: we deliberately construct the path RELATIVE
 * (`/api/themes/<name>`) instead of going through `apiUrl()`. `apiUrl()`
 * picks `http://localhost:8000` on the server (no `window`) and `""` on
 * the client, so the rendered href would differ between SSR HTML and the
 * post-hydration React tree. `<link rel="stylesheet">` resolves against
 * the document origin on both sides, so a relative path is identical and
 * is what we want. The dev rewrites in next.config.ts proxy `/api/*` to
 * the backend transparently.
 */
export function ThemeStylesheet() {
  const { site, themes } = useApp();
  const name = (site?.appearance as { style_theme?: string } | undefined)
    ?.style_theme;
  if (!name || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) return null;

  const theme = themes.find((t) => t.name === name);
  let url = `/api/themes/${encodeURIComponent(name)}`;
  if (theme?.updated_at) {
    const timestamp = new Date(theme.updated_at).getTime();
    if (!isNaN(timestamp)) {
      url += `?v=${timestamp}`;
    }
  }
  return <link rel="stylesheet" href={url} data-theme-stylesheet="active" />;
}
