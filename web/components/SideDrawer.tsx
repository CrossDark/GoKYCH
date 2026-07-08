"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { apiUrl } from "@/lib/api";

// SideDrawer — the left-rail navigation drawer.
//
// Opens via the ☰ button in the site header (to the right of the site
// title). Renders admin-managed sidebar_cards as a tightly-packed card
// grid; clicking a card navigates and closes the drawer.
//
// Data fetch — we hit /api/sidebar-cards the first time the drawer is
// opened in any given window. The result is cached in localStorage with
// a 5-minute TTL keyed by SiteAPI; subsequent opens within the TTL
// reuse the cached array, so the dashboard / article pages don't have
// to ship with the drawer state baked in. SSR does not pre-fetch cards
// because the drawer is a hover/click convenience (not part of the
// page body) and admin edits are visible within seconds via the
// frontend cache invalidation webhook anyway — there's no real
// correctness reason to bake it into HTML.
//
// Fetch URL goes through `apiUrl()` so the browser hits the backend
// origin directly (api.kych.net / api.ywda.net) instead of the frontend
// edge (eo.kych.net / cf.ywda.net). EdgeOne and Cloudflare Workers
// have no Next.js API route for `/api/sidebar-cards` (`web/app/api/`
// doesn't exist — this endpoint is served by the Go backend through
// nginx `/api/*` reverse proxy), so a relative path would hit the
// edge's default 504/500 fallback and stall the drawer on the
// "加载中…" placeholder for 30+ seconds. Absolute URLs skip the edge
// entirely. In dev `apiUrl()` returns "" on the client and
// "http://localhost:8000" on the server, but this fetch only runs in
// the browser (useEffect), so the SSR/client split is irrelevant here.

type SidebarCard = {
  id: number;
  title: string;
  url: string;
  icon: string;
  description: string;
  is_external: boolean;
};

const CACHE_KEY = "kokoro:sidebar-cards:v1";
const CACHE_TTL_MS = 5 * 60 * 1000;

type CacheShape = { ts: number; data: SidebarCard[] } | null;

function readCache(): CacheShape {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (typeof parsed.ts !== "number" || !Array.isArray(parsed.data)) return null;
    return parsed;
  } catch {
    return null;
  }
}

function writeCache(data: SidebarCard[]) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      CACHE_KEY,
      JSON.stringify({ ts: Date.now(), data })
    );
  } catch {
    // localStorage quota / disabled — silently skip; the next mount
    // will just re-fetch.
  }
}

export function SideDrawer({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [cards, setCards] = useState<SidebarCard[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);

  // Lazy fetch: only when the drawer is actually opened. The cache
  // read happens even before that so the open animation isn't gated
  // on a network round-trip after the first visit.
  useEffect(() => {
    if (!open || loaded) return;
    const cached = readCache();
    if (cached && Date.now() - cached.ts < CACHE_TTL_MS) {
      setCards(cached.data);
      setLoaded(true);
      return;
    }
    // Cache miss / stale — fetch fresh. Absolute URL routed through
    // apiUrl() so the request bypasses the frontend edge (which has no
    // /api/* route handler) and hits the backend origin directly.
    fetch(apiUrl("/api/sidebar-cards"), { credentials: "include" })
      .then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return (await r.json()) as SidebarCard[];
      })
      .then((data) => {
        setCards(data);
        setLoaded(true);
        setFailed(false);
        writeCache(data);
      })
      .catch(() => {
        setFailed(true);
        setLoaded(true);
      });
  }, [open, loaded]);

  // Lock body scroll while the drawer is open — same convention the
  // existing tag sidebar uses, so users can scroll neither page nor
  // drawer underneath an open modal-style drawer.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  // Close on Escape — minimal modal-style nicety; matches the tag
  // sidebar's interaction model so users don't have to learn a new
  // gesture for the left rail.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <>
      <div
        className={`side-drawer-overlay ${open ? "open" : ""}`}
        onClick={onClose}
        aria-hidden="true"
      />
      <aside className={`side-drawer ${open ? "open" : ""}`} aria-hidden={!open}>
        <div className="side-drawer-header">
          <h3>导航</h3>
          <button
            className="side-drawer-close"
            onClick={onClose}
            aria-label="关闭侧栏"
            type="button"
          >
            ✕
          </button>
        </div>
        <div className="side-drawer-body">
          {!loaded ? (
            <p className="side-drawer-empty">加载中…</p>
          ) : failed ? (
            <p className="side-drawer-empty">
              加载失败
              <br />
              <button type="button" className="side-drawer-retry" onClick={() => { setLoaded(false); setFailed(false); }}>
                重试
              </button>
            </p>
          ) : cards.length === 0 ? (
            <p className="side-drawer-empty">暂无卡片</p>
          ) : (
            <div className="side-drawer-grid">
              {cards.map((c) => (
                <Link
                  key={c.id}
                  href={c.url}
                  className="side-drawer-card"
                  target={c.is_external ? "_blank" : undefined}
                  rel={c.is_external ? "noopener noreferrer" : undefined}
                  onClick={onClose}
                  title={c.description || c.title}
                >
                  {c.icon ? <span className="side-drawer-card-icon" aria-hidden="true">{c.icon}</span> : null}
                  <span className="side-drawer-card-title">{c.title}</span>
                  {c.description ? (
                    <span className="side-drawer-card-desc">{c.description}</span>
                  ) : null}
                </Link>
              ))}
            </div>
          )}
        </div>
        <div className="side-drawer-footer">
          <span>由站点管理员配置</span>
        </div>
      </aside>
    </>
  );
}
