"use client";

import { Fragment } from "react";
import { useApp } from "./AppProviders";

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
  const cacheBust = theme?.updated_at
    ? `?v=${new Date(theme.updated_at).getTime()}`
    : "";
  const cssUrl = `/api/themes/${encodeURIComponent(name)}${cacheBust}`;

  // Theme-bundled JS assets. Add new entries here as more themes ship
  // their own scripts. Particles script is `defer` so it runs after CSS
  // is applied and after the body is parsed — it needs the body element
  // and the CSS variable to be live.
  const assets: { src: string; type: string }[] = [];
  if (name === "glass") {
    assets.push({ src: `/api/themes/glass/assets/effects/particles.js${cacheBust}`, type: "module-shim" });
  }

  return (
    <Fragment>
      <link rel="stylesheet" href={cssUrl} data-theme-stylesheet="active" />
      {assets.map((a) => (
        <script
          key={a.src}
          defer
          src={a.src}
          data-theme-asset={name}
        />
      ))}
    </Fragment>
  );
}

