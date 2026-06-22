"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { getCsrf, listArticles, listUsers, listNotifications } from "@/lib/api";
import type { ArticleListResult, User, Notification } from "@/lib/types";

export default function AdminDashboard() {
  const [csrf, setCsrf] = useState("");
  const [articles, setArticles] = useState<ArticleListResult | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [notifications, setNotifications] = useState<Notification[]>([]);

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      listArticles(undefined, 1).then(setArticles).catch(() => {});
      listUsers(r.csrf_token).then(setUsers).catch(() => {});
      listNotifications(r.csrf_token).then(setNotifications).catch(() => {});
    });
  }, []);

  return (
    <div className="admin-dashboard">
      <h1>仪表盘</h1>

      <div className="admin-stats-grid">
        <div className="admin-stat-card">
          <div className="admin-stat-number">{articles?.total ?? "—"}</div>
          <div className="admin-stat-label">文章总数</div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-number">{users.length}</div>
          <div className="admin-stat-label">用户数</div>
        </div>
        <div className="admin-stat-card">
          <div className="admin-stat-number">{notifications.length}</div>
          <div className="admin-stat-label">通知数</div>
        </div>
      </div>

      <div className="admin-quick-links">
        <h2>快捷操作</h2>
        <div className="admin-link-grid">
          <Link href="/admin/articles" className="admin-link-card">
            文章管理 → 新建 / 编辑 / 删除文章
          </Link>
          <Link href="/admin/users" className="admin-link-card">
            用户管理 → 创建 / 修改角色
          </Link>
          <Link href="/admin/notifications" className="admin-link-card">
            通知管理 → 发布 / 编辑通知
          </Link>
          <Link href="/admin/home" className="admin-link-card">
            首页管理 → 子站点链接 / 推荐文章
          </Link>
          <Link href="/admin/settings" className="admin-link-card">
            站点设置 → 标题 / 外观 / 功能开关
          </Link>
          <Link href="/admin/profile" className="admin-link-card">
            个人资料 → 修改昵称 / 头像
          </Link>
        </div>
      </div>
    </div>
  );
}
