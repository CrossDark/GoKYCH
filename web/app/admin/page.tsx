"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import {
  getCsrf,
  listArticles,
  listUsers,
  listNotifications,
  listAdminFiles,
} from "@/lib/api";
import type { Article, ArticleListResult, User, Notification, AdminFile } from "@/lib/types";

const ARTICLE_TYPES = [
  { key: "md", label: "Markdown", icon: "📝", color: "blue" },
  { key: "wikidot", label: "Wikidot", icon: "📚", color: "purple" },
  { key: "html", label: "HTML", icon: "🌐", color: "orange" },
  { key: "bbcode", label: "BBCode", icon: "📋", color: "teal" },
  { key: "typst", label: "Typst", icon: "📄", color: "green" },
] as const;

const QUICK_LINKS = [
  {
    href: "/admin/articles",
    icon: "📝",
    title: "文章管理",
    desc: "新建 / 编辑 / 删除文章",
    color: "blue",
  },
  {
    href: "/admin/users",
    icon: "👥",
    title: "用户管理",
    desc: "创建 / 修改角色 / 删除用户",
    color: "green",
  },
  {
    href: "/admin/notifications",
    icon: "🔔",
    title: "通知管理",
    desc: "发布 / 编辑 / 置顶通知",
    color: "orange",
  },
  {
    href: "/admin/home",
    icon: "🏠",
    title: "首页管理",
    desc: "子站点链接 / 推荐文章",
    color: "purple",
  },
  {
    href: "/admin/tags",
    icon: "🏷️",
    title: "标签管理",
    desc: "重命名 / 删除标签",
    color: "teal",
  },
  {
    href: "/admin/files",
    icon: "📁",
    title: "文件管理",
    desc: "上传 / 删除 / 引用文件",
    color: "red",
  },
  {
    href: "/admin/settings",
    icon: "⚙️",
    title: "站点设置",
    desc: "标题 / 外观 / 功能开关",
    color: "gray",
  },
  {
    href: "/admin/profile",
    icon: "👤",
    title: "个人资料",
    desc: "修改昵称 / 头像 / 简介",
    color: "blue",
  },
];

function formatRelative(iso: string): string {
  try {
    const d = new Date(iso);
    const now = new Date();
    const diff = (now.getTime() - d.getTime()) / 1000;
    if (diff < 60) return "刚刚";
    if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
    if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
    if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
    return d.toLocaleDateString("zh-CN");
  } catch { return "—"; }
}

export default function AdminDashboard() {
  const [csrf, setCsrf] = useState("");
  const [articles, setArticles] = useState<ArticleListResult | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [files, setFiles] = useState<AdminFile[]>([]);
  const [byType, setByType] = useState<Record<string, number>>({});
  const [recentArticles, setRecentArticles] = useState<Article[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      try {
        const r = await getCsrf();
        const token = r.csrf_token;
        setCsrf(token);

        // Per-type article counts (parallel) + recent articles
        const [all, md, wk, html, bb, typst, u, n, f] = await Promise.all([
          listArticles(undefined, 1).catch(() => null),
          listArticles("md", 1).catch(() => null),
          listArticles("wikidot", 1).catch(() => null),
          listArticles("html", 1).catch(() => null),
          listArticles("bbcode", 1).catch(() => null),
          listArticles("typst", 1).catch(() => null),
          listUsers(token).catch(() => []),
          listNotifications(token).catch(() => []),
          listAdminFiles(token).catch(() => []),
        ]);

        setArticles(all);
        setRecentArticles(all?.articles?.slice(0, 6) || []);
        setByType({
          md: md?.total ?? 0,
          wikidot: wk?.total ?? 0,
          html: html?.total ?? 0,
          bbcode: bb?.total ?? 0,
          typst: typst?.total ?? 0,
        });
        setUsers(u);
        setNotifications(n);
        setFiles(f);
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  const totalArticles = articles?.total ?? 0;
  const recentNotifs = notifications
    .slice()
    .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    .slice(0, 4);

  return (
    <div className="admin-dashboard">
      <div className="admin-page-header">
        <div>
          <h1>仪表盘</h1>
          <div className="admin-page-subtitle">站点的总览与快捷入口</div>
        </div>
      </div>

      {/* ── Stats overview ── */}
      <div className="admin-section-title">📊 概览</div>
      <div className="admin-stats-grid">
        <div className="admin-stat-card">
          <div className="admin-stat-icon blue">📝</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number">{loading ? "—" : totalArticles}</div>
            <div className="admin-stat-label">文章总数</div>
          </div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-icon green">👥</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number">{loading ? "—" : users.length}</div>
            <div className="admin-stat-label">用户数</div>
          </div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-icon orange">🔔</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number">{loading ? "—" : notifications.length}</div>
            <div className="admin-stat-label">通知数</div>
          </div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-icon red">📁</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number">{loading ? "—" : files.length}</div>
            <div className="admin-stat-label">文件数</div>
          </div>
        </div>
      </div>

      {/* ── Per-type breakdown ── */}
      <div className="admin-section-title">🗂 文章类型分布</div>
      <div className="admin-stats-grid">
        {ARTICLE_TYPES.map((t) => (
          <Link href={`/admin/articles?type=${t.key}`} key={t.key} className="admin-stat-card" style={{ textDecoration: "none", color: "inherit" }}>
            <div className={`admin-stat-icon ${t.color}`}>{t.icon}</div>
            <div className="admin-stat-body">
              <div className="admin-stat-number">{loading ? "—" : (byType[t.key] ?? 0)}</div>
              <div className="admin-stat-label">{t.label}</div>
            </div>
          </Link>
        ))}
      </div>

      {/* ── Recent articles + notifications ── */}
      <div className="admin-recent-grid">
        <div className="admin-card" style={{ marginBottom: 0 }}>
          <div className="admin-card-header">
            <h2>📄 最近文章</h2>
            <Link href="/admin/articles" className="admin-btn admin-btn-ghost admin-btn-sm">查看全部 →</Link>
          </div>
          <div className="admin-card-body no-padding">
            {loading ? (
              <div className="admin-empty">加载中…</div>
            ) : recentArticles.length === 0 ? (
              <div className="admin-empty">
                <span className="admin-empty-icon">📝</span>
                <div className="admin-empty-title">还没有文章</div>
                <div>去 <Link href="/admin/articles">文章管理</Link> 创建第一篇</div>
              </div>
            ) : (
              <table className="admin-table">
                <tbody>
                  {recentArticles.map((a) => (
                    <tr key={a.id}>
                      <td className="col-title" style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        <Link href={`/${a.type}/${a.slug}`} target="_blank" style={{ color: "inherit" }}>
                          {a.title}
                        </Link>
                      </td>
                      <td style={{ width: 80 }}>
                        <span className={`admin-badge admin-badge-${a.type === "md" ? "primary" : a.type === "wikidot" ? "danger" : a.type === "html" ? "warning" : a.type === "bbcode" ? "success" : "neutral"}`}>
                          {a.type}
                        </span>
                      </td>
                      <td className="col-date">{formatRelative(a.updated_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        <div className="admin-card" style={{ marginBottom: 0 }}>
          <div className="admin-card-header">
            <h2>🔔 最近通知</h2>
            <Link href="/admin/notifications" className="admin-btn admin-btn-ghost admin-btn-sm">查看全部 →</Link>
          </div>
          <div className="admin-card-body no-padding">
            {loading ? (
              <div className="admin-empty">加载中…</div>
            ) : recentNotifs.length === 0 ? (
              <div className="admin-empty">
                <span className="admin-empty-icon">🔔</span>
                <div className="admin-empty-title">还没有通知</div>
              </div>
            ) : (
              <table className="admin-table">
                <tbody>
                  {recentNotifs.map((n) => (
                    <tr key={n.id}>
                      <td className="col-title" style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {n.title}
                      </td>
                      <td style={{ width: 60 }}>
                        {n.is_important ? (
                          <span className="admin-badge admin-badge-danger">置顶</span>
                        ) : (
                          <span className="admin-badge admin-badge-neutral">普通</span>
                        )}
                      </td>
                      <td className="col-date">{formatRelative(n.updated_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>

      {/* ── Quick links ── */}
      <div className="admin-section-title">⚡ 快捷操作</div>
      <div className="admin-link-grid">
        {QUICK_LINKS.map((q) => (
          <Link key={q.href} href={q.href} className="admin-link-card">
            <div className={`admin-link-card-icon admin-stat-icon ${q.color}`}>{q.icon}</div>
            <div className="admin-link-card-body">
              <div className="admin-link-card-title">{q.title}</div>
              <div className="admin-link-card-desc">{q.desc}</div>
            </div>
          </Link>
        ))}
      </div>

      {/* ── System info ── */}
      <div className="admin-section-title">ℹ️ 系统信息</div>
      <div className="admin-stats-grid" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))" }}>
        <div className="admin-stat-card">
          <div className="admin-stat-icon gray">📅</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number" style={{ fontSize: "1.05rem", fontWeight: 600 }}>
              {new Date().toLocaleString("zh-CN", { month: "long", day: "numeric", weekday: "long" })}
            </div>
            <div className="admin-stat-label">当前时间</div>
          </div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-icon gray">⚙️</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number" style={{ fontSize: "1.05rem", fontWeight: 600 }}>Go + Next.js</div>
            <div className="admin-stat-label">站点技术栈</div>
          </div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-icon gray">🟢</div>
          <div className="admin-stat-body">
            <div className="admin-stat-number" style={{ fontSize: "1.05rem", fontWeight: 600 }}>运行中</div>
            <div className="admin-stat-label">服务状态</div>
          </div>
        </div>
      </div>
    </div>
  );
}
