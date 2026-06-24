"use client";

import { useEffect, useState } from "react";
import { getSite } from "@/lib/api";

/**
 * ThemeStylesheet — loads /api/themes/<style_theme>.css into the document
 * head, so the active theme's :root variables override the built-in defaults
 * from globals.css. If settings.yml has no style_theme (or the named
 * theme doesn't exist on disk), nothing is injected and globals.css wins.
 *
 * The settings endpoint is public, so we read it on mount. Cache-Control on
 * the theme CSS endpoint (5 min) handles the admin-edit case: when the
 * owner switches themes in /admin/settings, the next page load pulls the
 * new CSS automatically.
 */
export function ThemeStylesheet() {
  const [href, setHref] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getSite()
      .then((s) => {
        if (cancelled) return;
        const name = (s.appearance as any)?.style_theme;
        if (name && typeof name === "string" && /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name)) {
          // Append a build-time-ish buster to dodge the browser cache when
          // a user switches themes in another tab. The endpoint is already
          // cache-friendly (max-age=300), so this only matters across that
          // 5-min window.
          setHref(`/api/themes/${name}?v=${Date.now()}`);
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
