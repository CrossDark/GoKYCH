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
    return <div className="page"><p>加载中…</p></div>;
  }

  if (!user) {
    return <div className="page"><p>请先登录。</p></div>;
  }

  const links = [
    { href: "/admin", label: "仪表盘" },
    { href: "/admin/articles", label: "文章管理" },
    { href: "/admin/users", label: "用户管理" },
    { href: "/admin/notifications", label: "通知管理" },
    { href: "/admin/home", label: "首页管理" },
    { href: "/admin/settings", label: "站点设置" },
    { href: "/admin/profile", label: "个人资料" },
  ];

  return (
    <div className="admin-layout">
      <aside className="admin-sidebar">
        <h2 className="admin-sidebar-title">后台管理</h2>
        <nav className="admin-nav">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className={`admin-nav-link ${pathname === l.href ? "active" : ""}`}
            >
              {l.label}
            </Link>
          ))}
        </nav>
        <div className="admin-sidebar-footer">
          <span className="admin-user-info">
            {user.nickname || user.username}
            <span className="admin-user-role">{user.role}</span>
          </span>
          <button onClick={handleLogout} className="btn btn-small">
            退出
          </button>
        </div>
      </aside>
      <main className="admin-content">{children}</main>
    </div>
  );
}
