"use client";

import { useEffect, useState } from "react";
import { getSite, listThemes, apiUrl } from "@/lib/api";

/**
 * ThemeStylesheet — loads /api/themes/<style_theme>.css into the document
 * head, so the active theme's :root variables override the built-in defaults
 * from globals.css. If settings.yml has no style_theme (or the named
 * theme doesn't exist on disk), nothing is injected and globals.css wins.
 *
 * Uses the theme's updated_at timestamp as a cache-busting query parameter
 * (e.g. ?v=1719900000). This gives us:
 * - CDN/browser caching while the theme file is unchanged (same URL)
 * - Immediate cache invalidation when the CSS or theme.yaml is edited
 *   (new timestamp → new URL → cache miss)
 */
export function ThemeStylesheet() {
  const [href, setHref] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([getSite(), listThemes()])
      .then(([s, themes]) => {
        if (cancelled) return;
        const name = (s.appearance as any)?.style_theme;
        if (name && typeof name === "string" && /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) {
          const theme = themes.find((t) => t.name === name);
          let url = apiUrl(`/api/themes/${name}`);
          if (theme?.updated_at) {
            const timestamp = new Date(theme.updated_at).getTime();
            if (!isNaN(timestamp)) {
              url += `?v=${timestamp}`;
            }
          }
          setHref(url);
        } else {
          setHref(null);
        }
      })
      .catch(() => setHref(null));
    return () => { cancelled = true; };
  }, []);

  if (!href) return null;
  return <link rel="stylesheet" href={href} data-theme-stylesheet="active" />;
}
