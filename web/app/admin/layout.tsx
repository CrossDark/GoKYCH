"use client";

import { useState, useEffect, useRef } from "react";
import { useRouter, usePathname } from "next/navigation";
import Link from "next/link";
import { getMe, getCsrf, logout } from "@/lib/api";
import type { User } from "@/lib/types";

// Map pathname → breadcrumb label + optional parent
const BREADCRUMB: { match: (p: string) => boolean; label: string; parent?: string }[] = [
  { match: (p) => p === "/admin", label: "仪表盘", parent: "首页" },
  { match: (p) => p.startsWith("/admin/articles"), label: "文章管理", parent: "内容" },
  { match: (p) => p.startsWith("/admin/home"), label: "首页管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/tags"), label: "标签管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/files"), label: "文件管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/notifications"), label: "通知管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/users"), label: "用户管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/settings"), label: "站点设置", parent: "设置" },
  { match: (p) => p.startsWith("/admin/profile"), label: "个人资料", parent: "设置" },
];

function getBreadcrumb(pathname: string): { parent?: string; current: string } {
  const m = BREADCRUMB.find((b) => b.match(pathname));
  return m ? { parent: m.parent, current: m.label } : { current: "管理后台" };
}

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [csrfToken, setCsrfToken] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    getMe().then((r) => {
      if (!r.user || (r.user.role !== "admin" && r.user.role !== "owner")) {
        router.push("/auth/login?next=" + encodeURIComponent(pathname));
      } else {
        setUser(r.user);
        getCsrf().then((c) => setCsrfToken(c.csrf_token));
      }
    }).catch(() => {
      router.push("/auth/login?next=" + encodeURIComponent(pathname));
    }).finally(() => setLoading(false));
  }, []);

  // Close user menu on outside click
  useEffect(() => {
    if (!userMenuOpen) return;
    const handler = (e: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [userMenuOpen]);

  // Close user menu on route change
  useEffect(() => {
    setUserMenuOpen(false);
    setSidebarOpen(false);
  }, [pathname]);

  const handleLogout = async () => {
    await logout(csrfToken).catch(() => {});
    router.push("/");
  };

  if (loading) {
    return (
      <div className="wp-admin-wrap">
        <div className="wp-admin-main"><p>加载中…</p></div>
      </div>
    );
  }

  if (!user) {
    return (
      <div className="wp-admin-wrap">
        <div className="wp-admin-main"><p>请先登录，正在跳转…</p></div>
      </div>
    );
  }

  const menuGroups = [
    {
      label: "内容",
      items: [
        { href: "/admin", label: "仪表盘", icon: "📊" },
        { href: "/admin/articles", label: "文章管理", icon: "📝" },
      ],
    },
    {
      label: "管理",
      items: [
        { href: "/admin/home", label: "首页管理", icon: "🏠" },
        { href: "/admin/tags", label: "标签管理", icon: "🏷️" },
        { href: "/admin/files", label: "文件管理", icon: "📁" },
        { href: "/admin/notifications", label: "通知管理", icon: "🔔" },
        { href: "/admin/users", label: "用户管理", icon: "👥" },
      ],
    },
    {
      label: "设置",
      items: [
        { href: "/admin/settings", label: "站点设置", icon: "⚙️" },
        { href: "/admin/profile", label: "个人资料", icon: "👤" },
      ],
    },
  ];

  const crumb = getBreadcrumb(pathname);
  const avatarChar = user.nickname?.[0] || user.username[0] || "?";

  return (
    <div className="wp-admin-wrap">
      {/* Mobile hamburger */}
      <button
        className="wp-admin-hamburger"
        onClick={() => setSidebarOpen(!sidebarOpen)}
        aria-label="菜单"
      >
        {sidebarOpen ? "✕" : "☰"}
      </button>

      {/* Sidebar overlay for mobile */}
      {sidebarOpen && (
        <div className="wp-admin-overlay" onClick={() => setSidebarOpen(false)} />
      )}

      {/* Sidebar */}
      <aside className={`wp-admin-sidebar ${sidebarOpen ? "open" : ""}`}>
        <Link href="/" className="wp-admin-brand">
          <div className="wp-admin-brand-icon">KY</div>
          <div>
            <div className="wp-admin-brand-name">跨越晨昏</div>
            <div className="wp-admin-brand-desc">管理后台</div>
          </div>
        </Link>

        <nav className="wp-admin-nav">
          {menuGroups.map((group) => (
            <div key={group.label} className="wp-admin-nav-group">
              <div className="wp-admin-nav-label">{group.label}</div>
              {group.items.map((item) => {
                const active = pathname === item.href || (item.href !== "/admin" && pathname.startsWith(item.href));
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    className={`wp-admin-nav-link ${active ? "active" : ""}`}
                    onClick={() => setSidebarOpen(false)}
                  >
                    <span className="wp-admin-nav-icon" aria-hidden="true">{item.icon}</span>
                    <span className="wp-admin-nav-label-text">{item.label}</span>
                  </Link>
                );
              })}
            </div>
          ))}
        </nav>

        <div className="wp-admin-sidebar-footer">
          <Link href="/admin/profile" className="wp-admin-user" onClick={() => setSidebarOpen(false)}>
            <span className="wp-admin-user-avatar">{avatarChar}</span>
            <div>
              <div className="wp-admin-user-name">{user.nickname || user.username}</div>
              <div className="wp-admin-user-role">{user.role}</div>
            </div>
          </Link>
        </div>
      </aside>

      {/* Main content wrapper */}
      <div className="wp-admin-content">
        {/* Top bar */}
        <header className="admin-topbar">
          <div className="admin-topbar-left">
            <nav className="admin-topbar-breadcrumb" aria-label="breadcrumb">
              <Link href="/admin" className="admin-topbar-home" title="管理首页">⚙️</Link>
              {crumb.parent && (
                <>
                  <span className="sep">/</span>
                  <span>{crumb.parent}</span>
                </>
              )}
              <span className="sep">/</span>
              <span className="current">{crumb.current}</span>
            </nav>
          </div>
          <div className="admin-topbar-right">
            <Link href="/" className="admin-topbar-home" title="返回站点">
              ← 返回站点
            </Link>
            <div
              ref={userMenuRef}
              className={`admin-user-menu-wrap ${userMenuOpen ? "open" : ""}`}
            >
              <button
                className="admin-user-menu-btn"
                onClick={() => setUserMenuOpen(!userMenuOpen)}
                aria-haspopup="menu"
                aria-expanded={userMenuOpen}
              >
                <span className="admin-user-avatar">{avatarChar}</span>
                <span>{user.nickname || user.username}</span>
                <span className="admin-user-menu-arrow">▾</span>
              </button>
              <div className="admin-user-dropdown" role="menu">
                <div className="admin-user-dropdown-header">
                  <div className="admin-user-dropdown-name">{user.nickname || user.username}</div>
                  <div className="admin-user-dropdown-role">角色：{user.role}</div>
                </div>
                <Link href="/admin/profile" role="menuitem">
                  <span>👤</span><span>个人资料</span>
                </Link>
                <Link href="/" role="menuitem">
                  <span>🏠</span><span>返回站点</span>
                </Link>
                <div className="admin-user-dropdown-divider" />
                <button
                  className="admin-user-dropdown-danger"
                  onClick={handleLogout}
                  role="menuitem"
                >
                  <span>🚪</span><span>退出登录</span>
                </button>
              </div>
            </div>
          </div>
        </header>

        {/* Page content */}
        <main className="wp-admin-main">{children}</main>
      </div>
    </div>
  );
}
