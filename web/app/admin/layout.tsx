"use client";

import { useState, useEffect } from "react";
import { useRouter, usePathname } from "next/navigation";
import Link from "next/link";
import { getMe, getCsrf, logout } from "@/lib/api";
import type { User } from "@/lib/types";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [csrfToken, setCsrfToken] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);

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
        { href: "/admin", label: "📊 仪表盘" },
        { href: "/admin/articles", label: "📝 文章管理" },
      ],
    },
    {
      label: "管理",
      items: [
        { href: "/admin/home", label: "🏠 首页管理" },
        { href: "/admin/notifications", label: "🔔 通知管理" },
        { href: "/admin/users", label: "👥 用户管理" },
      ],
    },
    {
      label: "设置",
      items: [
        { href: "/admin/settings", label: "⚙️ 站点设置" },
        { href: "/admin/profile", label: "👤 个人资料" },
      ],
    },
  ];

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
              {group.items.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`wp-admin-nav-link ${pathname === item.href ? "active" : ""}`}
                  onClick={() => setSidebarOpen(false)}
                >
                  {item.label}
                </Link>
              ))}
            </div>
          ))}
        </nav>

        <div className="wp-admin-sidebar-footer">
          <div className="wp-admin-user">
            <span className="wp-admin-user-avatar">
              {user.nickname?.[0] || user.username[0]}
            </span>
            <div>
              <div className="wp-admin-user-name">{user.nickname || user.username}</div>
              <div className="wp-admin-user-role">{user.role}</div>
            </div>
          </div>
          <button onClick={handleLogout} className="wp-admin-logout-btn">
            退出登录
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className="wp-admin-main">{children}</main>
    </div>
  );
}
