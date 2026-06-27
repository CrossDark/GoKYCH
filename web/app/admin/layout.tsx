"use client";

import { useState, useEffect, useRef } from "react";
import { useRouter, usePathname } from "next/navigation";
import Link from "next/link";
import { getMe, getCsrf, logout, getSite } from "@/lib/api";
import type { User, SiteConfig } from "@/lib/types";
import { ToastProvider, useToast } from "@/lib/admin-feedback";
import { UserAvatar } from "@/components/admin/UserAvatar";

// Map pathname → breadcrumb label + optional parent. The "articles" entries
// are looked up dynamically because their label flips between "文章管理"
// (admin) and "我的文章" (regular user) — keeping the role-aware logic
// out of the static table avoids a second BREADCRUMB pass later.
const BREADCRUMB: { match: (p: string) => boolean; label: string; parent?: string }[] = [
  { match: (p) => p === "/admin", label: "仪表盘", parent: "首页" },
  { match: (p) => p.startsWith("/admin/home"), label: "首页管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/tags"), label: "标签管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/files"), label: "文件管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/notifications"), label: "通知管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/users"), label: "用户管理", parent: "管理" },
  { match: (p) => p.startsWith("/admin/settings"), label: "站点设置", parent: "设置" },
  { match: (p) => p.startsWith("/admin/api-keys"), label: "API Key", parent: "管理" },
  { match: (p) => p.startsWith("/admin/passkeys"), label: "Passkey", parent: "管理" },
  { match: (p) => p.startsWith("/admin/profile"), label: "个人资料", parent: "设置" },
];

function getBreadcrumb(pathname: string, role: string): { parent?: string; parentHref?: string; current: string } {
  // Article detail (deeper match first) — parent is role-aware.
  if (/^\/admin\/articles\/[^/]+\/[^/]+/.test(pathname)) {
    return {
      parent: role === "user" ? "我的文章" : "文章管理",
      parentHref: "/admin/articles",
      current: "文章详情",
    };
  }
  if (pathname === "/admin/articles" || pathname.startsWith("/admin/articles/")) {
    return {
      parent: "内容",
      current: role === "user" ? "我的文章" : "文章管理",
    };
  }
  const m = BREADCRUMB.find((b) => b.match(pathname));
  return m ? { parent: m.parent, current: m.label } : { current: "管理后台" };
}

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return (
    <ToastProvider>
      <AdminLayoutInner>{children}</AdminLayoutInner>
    </ToastProvider>
  );
}

function AdminLayoutInner({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const toast = useToast();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [csrfToken, setCsrfToken] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [site, setSite] = useState<SiteConfig["site"] | null>(null);
  const [brandLogoFailed, setBrandLogoFailed] = useState(false);
  const userMenuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    // Sidebar brand uses the same site logo as the public Header. Fetched
    // here rather than in the public Header so the admin SPA doesn't need
    // to share a context — both pages already pay for one /api/site each
    // and the response is cached at the EdgeOne edge for everyone.
    getSite().then((d) => setSite(d.site ?? null)).catch(() => {});
    getMe().then((r) => {
      if (!r.user) {
        router.push("/auth/login?next=" + encodeURIComponent(pathname));
        return;
      }
      // Anyone logged in can reach:
      //   - /admin/profile               (self-service)
      //   - /admin/articles              (regular users see only their own)
      //   - /admin/articles/[type]/[slug] (regular users can edit/delete
      //                                    articles they authored)
      // Every other /admin/* page is admin/owner only — regular users
      // would otherwise see admin dashboards they can't use, so bounce
      // them back to their profile.
      const isAdminLike = r.user.role === "admin" || r.user.role === "owner";
      const userAllowed =
        pathname === "/admin/profile" ||
        pathname === "/admin/articles" ||
        /^\/admin\/articles\/[^/]+\/[^/]+/.test(pathname);
      if (!isAdminLike && !userAllowed) {
        router.replace("/admin/profile");
        return;
      }
      setUser(r.user);
      getCsrf().then((c) => setCsrfToken(c.csrf_token));
    }).catch(() => {
      router.push("/auth/login?next=" + encodeURIComponent(pathname));
    }).finally(() => setLoading(false));
  }, []);

  // Close user menu on outside click
  useEffect(() => {
    // Reset brand-logo failure flag whenever the admin changes the
    // configured logo URL — otherwise a transient 404 would stick.
    setBrandLogoFailed(false);
  }, [site?.logo_path]);

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
    try {
      await logout(csrfToken);
      toast.success("已退出登录。");
    } catch {
      // Even if logout API fails, send the user back to the site.
    }
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

  // Regular users see their own profile plus a "我的文章" entry that
  // links to the (author-filtered) articles list. Admins/owners get the
  // full sidebar. The standalone /admin/passkeys entry is gone —
  // passkey management moved into /profile.
  const menuGroups = user.role === "user"
    ? [{
        label: "内容",
        items: [
          { href: "/admin/articles", label: "我的文章", icon: "📝" },
        ],
      }, {
        label: "账号",
        items: [{ href: "/admin/profile", label: "个人资料", icon: "👤" }],
      }]
    : [
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
            // Owner-only entries: API Key + 全站 Passkey 管理. Backend
            // gates both with requireOwner, so hide them from non-owners
            // to avoid a wall of 403s on direct nav.
            ...(user.role === "owner"
              ? [
                  { href: "/admin/api-keys", label: "API Key", icon: "🗝️" },
                  { href: "/admin/passkeys", label: "Passkey 管理", icon: "🛡️" },
                ]
              : []),
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

  const crumb = getBreadcrumb(pathname, user.role);

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
          {site?.logo_path && !brandLogoFailed ? (
            <img
              src={site.logo_path}
              alt={site.title || "site logo"}
              className="wp-admin-brand-icon"
              onError={() => setBrandLogoFailed(true)}
            />
          ) : (
            <div className="wp-admin-brand-icon wp-admin-brand-icon-fallback">KY</div>
          )}
          <div>
            <div className="wp-admin-brand-name">{site?.title || "跨越晨昏"}</div>
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
            <UserAvatar user={user} size={28} />
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
                  {crumb.parentHref ? (
                    <Link href={crumb.parentHref}>{crumb.parent}</Link>
                  ) : (
                    <span>{crumb.parent}</span>
                  )}
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
                <UserAvatar user={user} size={28} />
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
