"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useTheme } from "next-themes";
import type { User } from "@/lib/types";
import { getMe } from "@/lib/api";

export function Header() {
  const [user, setUser] = useState<User | null>(null);
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
    getMe()
      .then((r) => setUser(r.user))
      .catch(() => {});
  }, []);

  const toggleTheme = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };

  return (
    <header className="site-header">
      <div className="header-inner">
        <Link href="/" className="site-title">
          跨越晨昏
        </Link>
        <nav className="site-nav">
          <Link href="/md" className="nav-link">
            文章
          </Link>
          <Link href="/labels" className="nav-link">
            标签
          </Link>
          <Link href="/search" className="nav-link">
            搜索
          </Link>
          <Link href="/about" className="nav-link">
            关于
          </Link>
        </nav>
        <div className="header-actions">
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
  );
}
