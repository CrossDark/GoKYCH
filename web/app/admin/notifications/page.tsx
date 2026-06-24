"use client";

import { useState, useEffect } from "react";
import { getCsrf, listNotifications, createNotification, updateNotification, deleteNotification } from "@/lib/api";
import type { Notification } from "@/lib/types";

export default function AdminNotifications() {
  const [csrf, setCsrf] = useState("");
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [msg, setMsg] = useState<{ kind: "success" | "error"; text: string } | null>(null);
  const [editing, setEditing] = useState<Notification | null>(null);
  const [form, setForm] = useState({ title: "", content: "", is_important: false });
  const [submitting, setSubmitting] = useState(false);

  const load = () => {
    if (!csrf) return;
    listNotifications(csrf).then(setNotifications).catch(() => {});
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      load();
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [csrf]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(null);
    setSubmitting(true);
    try {
      await createNotification(csrf, { title: form.title, content: form.content, is_important: form.is_important });
      setMsg({ kind: "success", text: "通知已创建。" });
      setForm({ title: "", content: "", is_important: false });
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "创建失败。" });
    } finally {
      setSubmitting(false);
    }
  };

  const handleEdit = (n: Notification) => {
    setEditing(n);
    setForm({ title: n.title, content: n.content, is_important: n.is_important });
  };

  const handleUpdate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editing) return;
    setMsg(null);
    setSubmitting(true);
    try {
      await updateNotification(csrf, editing.id, { title: form.title, content: form.content, is_important: form.is_important });
      setMsg({ kind: "success", text: "通知已更新。" });
      setEditing(null);
      setForm({ title: "", content: "", is_important: false });
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "更新失败。" });
    } finally {
      setSubmitting(false);
    }
  };

  const handleToggleActive = async (n: any) => {
    const currentActive = n.is_active !== false;
    try {
      await updateNotification(csrf, n.id, { is_active: !currentActive });
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "操作失败。" });
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm("确定删除此通知吗？")) return;
    try {
      await deleteNotification(csrf, id);
      setMsg({ kind: "success", text: "通知已删除。" });
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "删除失败。" });
    }
  };

  return (
    <div className="admin-notifications">
      <div className="admin-page-header">
        <div>
          <h1>通知管理</h1>
          <div className="admin-page-subtitle">
            {notifications.length > 0 ? `共 ${notifications.length} 条通知` : "加载中…"}
          </div>
        </div>
      </div>

      {msg && (
        <div className={`admin-notice admin-notice-${msg.kind}`}>
          <span className="admin-notice-icon">{msg.kind === "success" ? "✓" : "✕"}</span>
          <div className="admin-notice-content">{msg.text}</div>
        </div>
      )}

      {/* Form */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>{editing ? `✏️ 编辑通知：${editing.title}` : "＋ 新建通知"}</h2>
          {editing && (
            <button className="admin-btn admin-btn-ghost admin-btn-sm" onClick={() => {
              setEditing(null);
              setForm({ title: "", content: "", is_important: false });
            }}>取消编辑</button>
          )}
        </div>
        <div className="admin-card-body">
          <form onSubmit={editing ? handleUpdate : handleCreate} className="admin-form">
            <div className="admin-form-group">
              <label>标题 <span style={{ color: "var(--admin-danger)" }}>*</span></label>
              <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} required maxLength={120} />
            </div>
            <div className="admin-form-group">
              <label>内容 <span style={{ color: "var(--admin-danger)" }}>*</span></label>
              <textarea
                value={form.content}
                onChange={(e) => setForm({ ...form, content: e.target.value })}
                rows={4}
                required
                className="admin-textarea-plain"
                placeholder="支持纯文本 / Markdown / HTML（取决于通知渲染器）"
              />
            </div>
            <div className="admin-form-group">
              <label style={{ display: "flex", alignItems: "center", gap: "0.5rem", cursor: "pointer", fontWeight: 500 }}>
                <input
                  type="checkbox"
                  checked={form.is_important}
                  onChange={(e) => setForm({ ...form, is_important: e.target.checked })}
                  style={{ width: "auto" }}
                />
                <span>⭐ 标记为重要通知（首页置顶显示）</span>
              </label>
            </div>
            <div className="admin-form-actions">
              <button type="submit" className="admin-btn admin-btn-primary" disabled={submitting}>
                {submitting ? "保存中…" : editing ? "保存修改" : "创建通知"}
              </button>
              <div className="admin-form-actions-spacer" />
              <span style={{ fontSize: "0.78rem", color: "var(--text-muted)" }}>
                通知会在首页 / 全站顶栏展示
              </span>
            </div>
          </form>
        </div>
      </div>

      {/* List */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>🔔 通知列表</h2>
        </div>
        <div className="admin-card-body no-padding">
          {notifications.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">🔔</span>
              <div className="admin-empty-title">还没有通知</div>
              <div>通过上方表单创建</div>
            </div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>标题</th>
                  <th>重要</th>
                  <th>状态</th>
                  <th className="col-date">更新时间</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {notifications.map((n) => {
                  const isActive = (n as any).is_active !== false;
                  return (
                    <tr key={n.id}>
                      <td className="col-title" style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {n.title}
                      </td>
                      <td>
                        {n.is_important ? (
                          <span className="admin-badge admin-badge-danger">⭐ 置顶</span>
                        ) : (
                          <span className="admin-badge admin-badge-neutral">普通</span>
                        )}
                      </td>
                      <td>
                        {isActive ? (
                          <span className="admin-badge admin-badge-success">活跃</span>
                        ) : (
                          <span className="admin-badge admin-badge-neutral">已关闭</span>
                        )}
                      </td>
                      <td className="col-date">{new Date(n.updated_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</td>
                      <td className="col-actions">
                        <button
                          className="admin-btn admin-btn-outline admin-btn-sm"
                          onClick={() => handleToggleActive(n)}
                          title={isActive ? "关闭" : "启用"}
                        >
                          {isActive ? "⏸" : "▶"}
                        </button>
                        <button className="admin-btn admin-btn-secondary admin-btn-sm" onClick={() => handleEdit(n)} title="编辑">✏️</button>
                        <button className="admin-btn admin-btn-danger admin-btn-sm" onClick={() => handleDelete(n.id)} title="删除">🗑</button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
