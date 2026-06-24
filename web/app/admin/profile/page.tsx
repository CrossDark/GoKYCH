"use client";

import { useState, useEffect } from "react";
import { getCsrf, getProfile, updateProfile } from "@/lib/api";
import type { User } from "@/lib/types";

export default function AdminProfile() {
  const [csrf, setCsrf] = useState("");
  const [user, setUser] = useState<User | null>(null);
  const [msg, setMsg] = useState<{ kind: "success" | "error"; text: string } | null>(null);
  const [form, setForm] = useState({ nickname: "", bio: "", avatar: "" });

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getProfile(r.csrf_token).then((u) => {
        setUser(u);
        setForm({ nickname: u.nickname || "", bio: u.bio || "", avatar: u.avatar || "" });
      }).catch(() => {});
    });
  }, []);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(null);
    try {
      const updated = await updateProfile(csrf, {
        nickname: form.nickname || undefined,
        bio: form.bio || undefined,
        avatar: form.avatar || undefined,
      });
      setUser(updated);
      setMsg({ kind: "success", text: "资料已更新。" });
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "保存失败。" });
    }
  };

  if (!user) return <div className="admin-profile"><div className="admin-card"><div className="admin-card-body"><div className="admin-empty">加载中…</div></div></div></div>;

  return (
    <div className="admin-profile">
      <div className="admin-page-header">
        <div>
          <h1>个人资料</h1>
          <div className="admin-page-subtitle">修改昵称、简介、头像</div>
        </div>
      </div>

      {msg && (
        <div className={`admin-notice admin-notice-${msg.kind}`}>
          <span className="admin-notice-icon">{msg.kind === "success" ? "✓" : "✕"}</span>
          <div className="admin-notice-content">{msg.text}</div>
        </div>
      )}

      {/* Account info (read-only) */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>👤 账号信息</h2>
        </div>
        <div className="admin-card-body">
          <div style={{ display: "flex", alignItems: "center", gap: "1rem", flexWrap: "wrap" }}>
            <div className="admin-user-avatar" style={{ width: 56, height: 56, fontSize: "1.2rem" }}>
              {(user.nickname?.[0] || user.username[0] || "?").toUpperCase()}
            </div>
            <div style={{ flex: 1, minWidth: 200 }}>
              <div style={{ fontWeight: 600, fontSize: "1rem" }}>{user.nickname || user.username}</div>
              <div style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 2 }}>
                @{user.username} · <span className={`admin-badge admin-badge-${user.role === "owner" ? "danger" : user.role === "admin" ? "warning" : "neutral"}`}>{user.role}</span>
              </div>
            </div>
            <div style={{ color: "var(--text-muted)", fontSize: "0.82rem" }}>
              注册于 {new Date(user.created_at).toLocaleDateString("zh-CN")}
            </div>
          </div>
        </div>
      </div>

      <form onSubmit={handleSave} className="admin-card" style={{ marginTop: "1.25rem" }}>
        <div className="admin-card-header">
          <h2>✏️ 编辑资料</h2>
        </div>
        <div className="admin-card-body">
          <div className="admin-form">
            <div className="admin-form-group">
              <label>昵称</label>
              <input
                value={form.nickname}
                onChange={(e) => setForm({ ...form, nickname: e.target.value })}
                placeholder="显示在评论 / 文章中的名字"
              />
            </div>
            <div className="admin-form-group">
              <label>简介</label>
              <textarea
                value={form.bio}
                onChange={(e) => setForm({ ...form, bio: e.target.value })}
                rows={4}
                className="admin-textarea-plain"
                placeholder="一句话介绍自己…"
              />
            </div>
            <div className="admin-form-group">
              <label>头像 URL</label>
              <input
                value={form.avatar}
                onChange={(e) => setForm({ ...form, avatar: e.target.value })}
                placeholder="https://... 或 /uploads/xxx"
              />
              <div className="admin-form-hint">支持外链或站内上传文件</div>
            </div>
            <div className="admin-form-actions">
              <button type="submit" className="admin-btn admin-btn-primary">保存修改</button>
            </div>
          </div>
        </div>
      </form>
    </div>
  );
}
