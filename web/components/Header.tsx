"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import Link from "next/link";
import { useTheme } from "next-themes";
import type { SubsiteLink, TagWithCount } from "@/lib/types";
import { listLabels } from "@/lib/api";
import { useApp } from "./AppProviders";
import { UserAvatar } from "@/components/admin/UserAvatar";

const LABELS_CACHE_TTL = 5 * 60 * 1000;

export function Header() {
  const { site, labels: ctxLabels, user } = useApp();
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // Tag sidebar state. ctxLabels (SSR-fetched by <AppData>) seeds both the
  // visible list and the in-memory TTL cache so opening the sidebar no
  // longer fires a client fetch within the cache window.
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [tags, setTags] = useState<TagWithCount[]>(ctxLabels);
  const tagsCacheRef = useRef<{ data: TagWithCount[]; timestamp: number } | null>(null);

  // Subsite links + site settings come straight from the SSR context — no
  // more getSite() on mount.
  const subsiteLinks: SubsiteLink[] = site?.subsite_links ?? [];
  const siteSetting = site?.site ?? null;
  // When site.logo_path points at a 404 (older settings still carry
  // "/static/img/logo.png" which the SPA origin can't serve) we don't want
  // a broken-image icon in the header — flip to the 🌅 fallback instead.
  // Reset on URL change so editing the setting in /admin/settings kicks in.
  const [logoFailed, setLogoFailed] = useState(false);
  useEffect(() => {
    setLogoFailed(false);
    // Keep the visible tag list in sync when SSR context refreshes (e.g.
    // after an on-demand revalidate swaps in newer data on navigation).
    setTags(ctxLabels);
    tagsCacheRef.current = { data: ctxLabels, timestamp: Date.now() };
  }, [ctxLabels, siteSetting?.logo_path]);

  const fetchLabels = useCallback(() => {
    const now = Date.now();
    if (
      tagsCacheRef.current &&
      now - tagsCacheRef.current.timestamp < LABELS_CACHE_TTL
    ) {
      setTags(tagsCacheRef.current.data);
      return Promise.resolve();
    }
    return listLabels()
      .then((data) => {
        tagsCacheRef.current = { data, timestamp: now };
        setTags(data);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    setMounted(true);
  }, []);

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };

  const openSidebar = useCallback(() => {
    setSidebarOpen(true);
    fetchLabels();
    document.body.style.overflow = "hidden";
  }, [fetchLabels]);

  const closeSidebar = useCallback(() => {
    setSidebarOpen(false);
    document.body.style.overflow = "";
  }, []);

  // Cleanup body overflow on unmount in case sidebar was open when navigating away
  useEffect(() => {
    return () => {
      document.body.style.overflow = "";
    };
  }, []);

  return (
    <>
      <header className="site-header">
        <div className="header-inner">
          <Link href="/" className="site-title">
            {siteSetting?.logo_path && !logoFailed ? (
              // Admin can upload a custom logo under 「站点信息 → Logo 路径」;
              // the field is /uploads/... or an absolute URL. onError flips
              // logoFailed → fallback 🌅 pill renders instead.
              <img
                src={siteSetting.logo_path}
                alt={siteSetting.title || "site logo"}
                className="site-logo"
                onError={() => setLogoFailed(true)}
              />
            ) : (
              <span className="site-logo-fallback" aria-hidden="true">🌅</span>
            )}
            <span>{siteSetting?.title || "跨越晨昏"}</span>
          </Link>
          {/* Middle: admin-editable subsite links */}
          <nav className="header-subsites">
            {subsiteLinks.map((link) => (
              <a
                key={link.url}
                href={link.url}
                className="nav-link subsite-link"
                title={link.description || link.name}
                target={link.url.startsWith("http") ? "_blank" : undefined}
                rel={link.url.startsWith("http") ? "noopener noreferrer" : undefined}
              >
                {link.name}
              </a>
            ))}
          </nav>
          {/* Right: tags, search, theme, user */}
          <div className="header-actions">
            <button
              onClick={openSidebar}
              className="nav-link sidebar-toggle-btn"
              aria-label="标签列表"
              title="标签"
            >
              🏷️
            </button>
            <Link href="/search" className="nav-link" title="搜索">
              🔍
            </Link>
            {mounted && (
              <button
                onClick={toggleTheme}
                className="theme-toggle"
                aria-label="切换主题"
                title={theme === "dark" ? "切换到亮色模式" : "切换到暗色模式"}
              >
                {theme === "dark" ? "☀️" : "🌙"}
              </button>
            )}
            {user ? (
              <Link href="/admin" className="nav-link user-link" title={`${user.nickname || user.username} · 进入管理后台`}>
                <UserAvatar user={user} size={28} />
                <span>{user.nickname || user.username}</span>
              </Link>
            ) : (
              <Link href="/auth/login" className="nav-link">
                登录
              </Link>
            )}
          </div>
        </div>
      </header>

      {/* Tag Sidebar Overlay */}
      <div
        className={`sidebar-overlay ${sidebarOpen ? "open" : ""}`}
        onClick={closeSidebar}
        aria-hidden="true"
      />

      {/* Tag Sidebar */}
      <aside className={`sidebar ${sidebarOpen ? "open" : ""}`}>
        <div className="sidebar-header">
          <h3>🏷️ 标签</h3>
          <button
            className="sidebar-close"
            onClick={closeSidebar}
            aria-label="关闭标签列表"
          >
            ✕
          </button>
        </div>
        <div className="sidebar-body">
          {tags.length === 0 ? (
            <p className="sidebar-empty">暂无标签</p>
          ) : (
            tags.map((tag) => (
              <Link
                key={tag.id}
                href={`/labels/${tag.name}`}
                className="sidebar-tag"
                onClick={closeSidebar}
              >
                {tag.name}
                <span className="sidebar-tag-count">{tag.count}</span>
              </Link>
            ))
          )}
        </div>
        <div className="sidebar-footer">
          <Link href="/labels" onClick={closeSidebar}>
            查看全部标签 →
          </Link>
        </div>
      </aside>
    </>
  );
}
