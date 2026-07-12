"use client";

import { useEffect } from "react";
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
 * every mount. It now reads both from the <AppData> SSR context.
 *
 * The theme's updated_at timestamp is appended as ?v= for cache-busting
 * (same URL while unchanged → CDN/browser cache; new timestamp on edit →
 * cache miss).
 *
 * ── Why this returns null + imperatively appends to <head> ─────────
 *
 * `<link>` / `<script>` for theme CSS and bundled assets are mounted
 * via direct DOM manipulation in a useEffect rather than rendered as
 * React elements. Two reasons:
 *
 * 1. The frontend edge (eo.kych.net / cf.ywda.net) has no Next.js API
 *    route for `/api/themes/*` (`web/app/api/` doesn't exist — these
 *    endpoints are served by the Go backend through nginx `/api/*`
 *    reverse proxy), and there's no way to add one: next.config.ts's
 *    dev-only rewrite proxying `/api/*` to localhost:8000 means a
 *    Next.js API route at the same path is unreachable in dev (the
 *    rewrite wins). So a browser-side relative path would hit the
 *    edge's default 504/500 fallback and stall the page render for
 *    30+ seconds. `apiUrl()` returns an absolute backend URL, which
 *    bypasses the edge — but the absolute URL differs between SSR
 *    (`http://localhost:8000` in dev) and client (`""` in dev),
 *    producing a hydration mismatch on the rendered href.
 *
 * 2. Returning null from this component on both SSR and the first
 *    client render means React has nothing to diff; the `<link>` is
 *    appended to <head> after hydration completes. Production
 *    `NEXT_PUBLIC_API_BASE_URL` is identical on both sides, so a
 *    one-frame FOUC is the only cost — and only in dev (where the
 *    relative path that would have matched in dev is, by design,
 *    dropped).
 */
export function ThemeStylesheet() {
  const { site, themes } = useApp();
  const name = (site?.appearance as { style_theme?: string } | undefined)
    ?.style_theme;

  useEffect(() => {
    if (!name || !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) return;
    if (typeof document === "undefined") return;

    const theme = themes.find((t) => t.name === name);
    const cacheBust = theme?.updated_at
      ? `?v=${new Date(theme.updated_at).getTime()}`
      : "";
    const cssHref = `${apiUrl(`/api/themes/${encodeURIComponent(name)}`)}${cacheBust}`;
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = cssHref;
    link.dataset.themeStylesheet = "active";
    document.head.appendChild(link);

    const apiBase = apiUrl("/").replace(/\/$/, "");
    (window as unknown as { __GK_API_BASE_URL?: string }).__GK_API_BASE_URL = apiBase;

    // Theme-bundled effect scripts — each theme loads its own script if available.
    // The scripts are deferred so the browser executes them after CSS is applied.
    const scripts: HTMLScriptElement[] = [];
    if (name === "glass") {
      const s = document.createElement("script");
      s.defer = true;
      s.src = `${apiUrl(`/api/themes/glass/assets/effects/particles.js`)}${cacheBust}`;
      s.dataset.themeAsset = name;
      document.head.appendChild(s);
      scripts.push(s);
    }
    if (name === "abyss-stage") {
      const s = document.createElement("script");
      s.defer = true;
      s.src = `${apiUrl(`/api/themes/abyss-stage/assets/effects/abyss-fx.js`)}${cacheBust}`;
      s.dataset.themeAsset = name;
      document.head.appendChild(s);
      scripts.push(s);
    }

    return () => {
      link.remove();
      scripts.forEach((s) => s.remove());
    };
  }, [name, themes]);

  return null;
}