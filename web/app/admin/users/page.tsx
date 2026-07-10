"use client";

import { useState, useEffect } from "react";
import { getCsrf, listUsers, createUser, updateUserRole, deleteUser } from "@/lib/api";
import type { User } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { UserAvatar } from "@/components/admin/UserAvatar";
import { fmtDate } from "@/lib/format";

const ROLE_BADGE: Record<string, string> = {
  owner: "danger",
  admin: "warning",
  user: "neutral",
};

const ROLE_LABEL: Record<string, string> = {
  owner: "站长",
  admin: "管理员",
  user: "用户",
};

export default function AdminUsers() {
  const [csrf, setCsrf] = useState("");
  const [users, setUsers] = useState<User[]>([]);
  const toast = useToast();
  const [search, setSearch] = useState("");
  const [form, setForm] = useState({ username: "", nickname: "", role: "user" });
  const [generatedCredential, setGeneratedCredential] = useState<{ username: string; password: string } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const isDirty = !!(form.username || form.nickname);

  const load = () => {
    if (!csrf) return;
    listUsers(csrf).then(setUsers).catch((e) => toast.error(e.message));
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      listUsers(r.csrf_token).then(setUsers).catch(() => {});
    });
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      const result = await createUser(csrf, {
        username: form.username,
        nickname: form.nickname || undefined,
        role: form.role,
      });
      const username = result.user?.username || form.username;
      setGeneratedCredential({ username, password: result.password });
      toast.success(`用户「${username}」已创建，请立即保存随机密码。`);
      setForm({ username: "", nickname: "", role: "user" });
      load();
    } catch (err: any) {
      toast.error(err.message || "创建失败。");
    } finally {
      setSubmitting(false);
    }
  };

  const handleRoleChange = async (username: string, role: string) => {
    try {
      await updateUserRole(csrf, username, role);
      toast.success(`已更新「${username}」的角色。`);
      load();
    } catch (err: any) {
      toast.error(err.message || "操作失败。");
    }
  };

  const handleDelete = (username: string) => setPendingDelete(username);
  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const username = pendingDelete;
    try {
      await deleteUser(csrf, username);
      toast.success(`用户「${username}」已删除。`);
      load();
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err;
    } finally {
      setPendingDelete(null);
    }
  };

  const copyGeneratedPassword = async () => {
    if (!generatedCredential) return;
    try {
      await navigator.clipboard.writeText(generatedCredential.password);
      toast.success("已复制随机密码。");
    } catch {
      toast.warning("复制失败，请手动选中密码复制。");
    }
  };

  const filtered = users.filter((u) => {
    if (!search) return true;
    const s = search.toLowerCase();
    return u.username.toLowerCase().includes(s)
      || (u.nickname || "").toLowerCase().includes(s);
  });

  useBeforeUnload((isDirty && !submitting) || !!generatedCredential);

  return (
    <div className="admin-users">
      <div className="admin-page-header">
        <div>
          <h1>用户管理</h1>
          <div className="admin-page-subtitle">共 {users.length} 个用户{search && ` · 匹配 ${filtered.length}`}</div>
        </div>
      </div>

      {/* New user form */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>＋ 创建新用户</h2>
        </div>
        <div className="admin-card-body">
          <form onSubmit={handleCreate} className="admin-form">
            <div className="admin-form-row">
              <div className="admin-form-group">
                <label htmlFor="user-username">
                  用户名
                  <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
                </label>
                <input
                  id="user-username"
                  value={form.username}
                  onChange={(e) => setForm({ ...form, username: e.target.value })}
                  required
                  placeholder="字母、数字、汉字等 Unicode 字符"
                  autoComplete="username"
                  aria-describedby="user-username-hint"
                />
                <div id="user-username-hint" className="admin-form-hint">
                  不再限制用户名长度；仍只允许字母、数字以及 . _ -，避免 URL 和日志里出现危险字符。
                </div>
              </div>
            </div>
            <div className="admin-form-row">
              <div className="admin-form-group">
                <label htmlFor="user-nickname">昵称</label>
                <input
                  id="user-nickname"
                  value={form.nickname}
                  onChange={(e) => setForm({ ...form, nickname: e.target.value })}
                  placeholder="显示名称（可选）"
                />
              </div>
              <div className="admin-form-group">
                <label htmlFor="user-role">角色</label>
                <select
                  id="user-role"
                  value={form.role}
                  onChange={(e) => setForm({ ...form, role: e.target.value })}
                  aria-describedby="user-role-hint"
                >
                  <option value="user">用户（普通）</option>
                  <option value="admin">管理员</option>
                  <option value="owner">站长（最高权限）</option>
                </select>
                <div id="user-role-hint" className="admin-form-hint">
                  站长可以管理其他用户和站点设置
                </div>
              </div>
            </div>
            <div className="admin-form-actions">
              <button
                type="submit"
                className={`admin-btn admin-btn-primary ${submitting ? "admin-btn-loading" : ""}`}
                disabled={submitting}
              >
                创建并生成随机密码
              </button>
            </div>
          </form>
        </div>
      </div>

      {generatedCredential && (
        <div className="admin-notice admin-notice-success">
          <span className="admin-notice-icon">🔐</span>
          <div className="admin-notice-content">
            <strong>用户「{generatedCredential.username}」的随机密码只显示这一次：</strong>
            <div className="admin-generated-password" style={{ marginTop: 8 }}>
              <code>{generatedCredential.password}</code>
              <button type="button" className="admin-btn admin-btn-outline admin-btn-sm" onClick={copyGeneratedPassword}>复制</button>
              <button type="button" className="admin-btn admin-btn-ghost admin-btn-sm" onClick={() => setGeneratedCredential(null)}>我已保存</button>
            </div>
          </div>
        </div>
      )}

      {/* Filter */}
      <div className="admin-filter">
        <span className="admin-filter-label">搜索：</span>
        <div className="admin-search-box" style={{ maxWidth: 300 }}>
          <span className="admin-search-box-icon">🔍</span>
          <input
            placeholder="按用户名 / 昵称搜索"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* List */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>👥 用户列表</h2>
        </div>
        <div className="admin-card-body no-padding">
          {users.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">👥</span>
              <div className="admin-empty-title">加载中…</div>
            </div>
          ) : filtered.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">🔍</span>
              <div className="admin-empty-title">没有匹配的用户</div>
            </div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th className="col-id">ID</th>
                  <th>用户名</th>
                  <th>昵称</th>
                  <th>角色</th>
                  <th className="col-date">创建时间</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((u) => (
                  <tr key={u.id}>
                    <td className="col-id">{u.id}</td>
                    <td className="col-title">
                      <span style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem" }}>
                        <UserAvatar user={u} size={24} />
                        {u.username}
                      </span>
                    </td>
                    <td>{u.nickname || <span style={{ color: "var(--text-muted)" }}>—</span>}</td>
                    <td>
                      <select
                        value={u.role}
                        onChange={(e) => handleRoleChange(u.username, e.target.value)}
                        className="admin-role-select"
                        style={{ fontSize: "0.82rem", padding: "0.25rem 0.5rem" }}
                      >
                        <option value="user">用户</option>
                        <option value="admin">管理员</option>
                        <option value="owner">站长</option>
                      </select>
                      <span className={`admin-badge admin-badge-${ROLE_BADGE[u.role] || "neutral"}`} style={{ marginLeft: "0.4rem" }}>
                        {ROLE_LABEL[u.role] || u.role}
                      </span>
                    </td>
                    <td className="col-date">{fmtDate(u.created_at)}</td>
                    <td className="col-actions">
                      <div className="admin-table-actions">
                        <button
                          className="admin-btn admin-btn-danger admin-btn-sm"
                          onClick={() => handleDelete(u.username)}
                          title="删除"
                        >
                          🗑
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <AdminConfirm
        open={!!pendingDelete}
        title="删除用户"
        message={pendingDelete ? `确定要删除用户「${pendingDelete}」吗？此操作不可撤销。` : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}
