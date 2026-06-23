"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { useTheme } from "next-themes";
import type { User, SubsiteLink } from "@/lib/types";
import { getMe, listLabels, getSite } from "@/lib/api";
import type { TagWithCount } from "@/lib/types";

export function Header() {
  const [user, setUser] = useState<User | null>(null);
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // Tag sidebar state
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [tags, setTags] = useState<TagWithCount[]>([]);

  // Subsite links (admin-editable nav links, served from /api/site so we
  // don't double-fetch with the homepage).
  const [subsiteLinks, setSubsiteLinks] = useState<SubsiteLink[]>([]);

  useEffect(() => {
    setMounted(true);
    getMe()
      .then((r) => setUser(r.user))
      .catch(() => {});
    // Preload tags
    listLabels()
      .then(setTags)
      .catch(() => {});
    // Load subsite links (and other nav config) from /api/site — one
    // round-trip covers title/theme/footer-ICP/subsite_links for the
    // whole header+layout.
    getSite()
      .then((d) => setSubsiteLinks(d.subsite_links ?? []))
      .catch(() => {});
  }, []);

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };

  const openSidebar = useCallback(() => {
    setSidebarOpen(true);
    // Refresh tags
    listLabels()
      .then(setTags)
      .catch(() => {});
    document.body.style.overflow = "hidden";
  }, []);

  const closeSidebar = useCallback(() => {
    setSidebarOpen(false);
    document.body.style.overflow = "";
  }, []);

  return (
    <>
      <header className="site-header">
        <div className="header-inner">
          <Link href="/" className="site-title">
            跨越晨昏
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
              <Link href="/admin" className="nav-link user-link">
                {user.nickname || user.username}
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
                href={`/labels/${encodeURIComponent(tag.name)}`}
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
