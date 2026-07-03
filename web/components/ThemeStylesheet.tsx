"use client";

import { useApp } from "./AppProviders";
import { apiUrl } from "@/lib/api";

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
 */
export function ThemeStylesheet() {
  const { site, themes } = useApp();
  const name = (site?.appearance as { style_theme?: string } | undefined)
    ?.style_theme;
  if (!name || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) return null;

  const theme = themes.find((t) => t.name === name);
  let url = apiUrl(`/api/themes/${name}`);
  if (theme?.updated_at) {
    const timestamp = new Date(theme.updated_at).getTime();
    if (!isNaN(timestamp)) {
      url += `?v=${timestamp}`;
    }
  }
  return <link rel="stylesheet" href={url} data-theme-stylesheet="active" />;
}
