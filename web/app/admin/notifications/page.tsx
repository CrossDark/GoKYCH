"use client";

import { useState, useEffect } from "react";
import { getCsrf, listNotifications, createNotification, updateNotification, deleteNotification } from "@/lib/api";
import type { Notification } from "@/lib/types";

export default function AdminNotifications() {
  const [csrf, setCsrf] = useState("");
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [msg, setMsg] = useState("");
  const [editing, setEditing] = useState<Notification | null>(null);
  const [form, setForm] = useState({ title: "", content: "", is_important: false });

  const load = () => {
    if (!csrf) return;
    listNotifications(csrf).then(setNotifications).catch(() => {});
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      load();
    });
  }, [csrf]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg("");
    try {
      await createNotification(csrf, { title: form.title, content: form.content, is_important: form.is_important });
      setMsg("通知已创建。");
      setForm({ title: "", content: "", is_important: false });
      load();
    } catch (err: any) {
      setMsg(err.message || "创建失败。");
    }
  };

  const handleEdit = (n: Notification) => {
    setEditing(n);
    setForm({ title: n.title, content: n.content, is_important: n.is_important });
  };

  const handleUpdate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editing) return;
    setMsg("");
    try {
      await updateNotification(csrf, editing.id, { title: form.title, content: form.content, is_important: form.is_important });
      setMsg("通知已更新。");
      setEditing(null);
      setForm({ title: "", content: "", is_important: false });
      load();
    } catch (err: any) {
      setMsg(err.message || "更新失败。");
    }
  };

  const handleToggleActive = async (n: any) => {
    const currentActive = n.is_active !== false;
    try {
      await updateNotification(csrf, n.id, { is_active: !currentActive });
      load();
    } catch (err: any) {
      setMsg(err.message || "操作失败。");
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm("确定删除此通知吗？")) return;
    try {
      await deleteNotification(csrf, id);
      setMsg("通知已删除。");
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  return (
    <div className="admin-notifications">
      <h1>通知管理</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      <details className="admin-form-section">
        <summary>{editing ? `编辑：${editing.title}` : "新建通知"}</summary>
        <form onSubmit={editing ? handleUpdate : handleCreate} className="admin-form">
          <label>标题 <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} required /></label>
          <label>内容 <textarea value={form.content} onChange={(e) => setForm({ ...form, content: e.target.value })} rows={3} required /></label>
          <label className="admin-checkbox-label">
            <input type="checkbox" checked={form.is_important} onChange={(e) => setForm({ ...form, is_important: e.target.checked })} />
            重要通知
          </label>
          <div className="admin-form-actions">
            <button type="submit" className="btn btn-primary">{editing ? "保存" : "创建"}</button>
            {editing && <button type="button" className="btn" onClick={() => { setEditing(null); setForm({ title: "", content: "", is_important: false }); }}>取消</button>}
          </div>
        </form>
      </details>

      {notifications.length === 0 ? (
        <p className="empty-message">暂无通知。</p>
      ) : (
        <table className="admin-table">
          <thead>
            <tr><th>标题</th><th>重要</th><th>状态</th><th>更新时间</th><th>操作</th></tr>
          </thead>
          <tbody>
            {notifications.map((n) => (
              <tr key={n.id}>
                <td>{n.title}</td>
                <td>{n.is_important ? "⭐" : "—"}</td>
                <td>{(n as any).is_active !== false ? "活跃" : "已关闭"}</td>
                <td>{new Date(n.updated_at).toLocaleDateString("zh-CN")}</td>
                <td>
                  <button className="btn btn-small" onClick={() => handleEdit(n)}>编辑</button>
                  <button className="btn btn-small btn-danger" onClick={() => handleDelete(n.id)}>删除</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
