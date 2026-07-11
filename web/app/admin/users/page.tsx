"use client";

import { useState, useEffect } from "react";
import {
  getCsrf, listUsers, createUser, updateUserRole, deleteUser,
  forceResetUserPassword, immediateResetUserPassword, forceLogoutUser,
} from "@/lib/api";
import type { User } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { AdminModal } from "@/components/admin/AdminModal";
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
  const [currentUser, setCurrentUser] = useState<User | null>(null);
  const toast = useToast();
  const [search, setSearch] = useState("");
  const [form, setForm] = useState({ username: "", nickname: "", role: "user" });
  const [generatedCredential, setGeneratedCredential] = useState<{ username: string; password: string } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  // Lifecycle action state. The "kind" discriminates which operation
  // the modal is for; targetUsername is the user being acted on. Only
  // one of these is open at a time.
  const [pendingAction, setPendingAction] = useState<
    | { kind: "force-reset"; username: string }
    | { kind: "immediate-reset"; username: string }
    | { kind: "force-logout"; username: string }
    | null
  >(null);
  // Sticky modal for the new plaintext after an immediate-reset. We
  // deliberately don't put the plaintext in a toast — too easy to miss
  // before auto-dismiss, and the admin is expected to copy and deliver
  // it to the user out-of-band.
  const [revealedPassword, setRevealedPassword] = useState<{ username: string; password: string } | null>(null);
  const isDirty = !!(form.username || form.nickname);

  const load = () => {
    if (!csrf) return;
    listUsers(csrf).then(setUsers).catch((e) => toast.error(e.message));
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      listUsers(r.csrf_token).then(setUsers).catch(() => {});
      // Determine the current logged-in user by fetching /auth/me with
      // the same CSRF. We need this to disable self-actions (don't
      // reset my own password from the admin page) and to gate the
      // owner-row actions (can't reset the owner's password).
      import("@/lib/api").then((m) => m.getMe()).then((r) => {
        if (r && r.user) setCurrentUser(r.user as User);
      }).catch(() => {});
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

  const copyRevealedPassword = async () => {
    if (!revealedPassword) return;
    try {
      await navigator.clipboard.writeText(revealedPassword.password);
      toast.success("已复制新密码。");
    } catch {
      toast.warning("复制失败，请手动选中密码复制。");
    }
  };

  // confirmAction runs the chosen lifecycle action. Errors are
  // surfaced via toast; the only thing we special-case is
  // immediate-reset, whose response carries the new plaintext we
  // need to surface in a sticky modal.
  const confirmAction = async () => {
    if (!pendingAction) return;
    const act = pendingAction;
    try {
      if (act.kind === "force-reset") {
        await forceResetUserPassword(csrf, act.username);
        toast.success(`已标记「${act.username}」强制重置：用户下次登录时将自动生成新密码。`);
      } else if (act.kind === "force-logout") {
        await forceLogoutUser(csrf, act.username);
        toast.success(`已强制「${act.username}」退出登录。`);
      } else if (act.kind === "immediate-reset") {
        const r = await immediateResetUserPassword(csrf, act.username);
        setRevealedPassword({ username: act.username, password: r.password });
        toast.success(`已立即重置「${act.username}」的密码，新密码已生成。`);
      }
      load();
    } catch (err: any) {
      toast.error(err.message || "操作失败。");
      throw err; // keep confirm modal open so the user can retry
    } finally {
      if (act.kind !== "immediate-reset") setPendingAction(null);
      // For immediate-reset, we close the confirm modal and let the
      // sticky "revealedPassword" modal take over. The user dismisses
      // that one explicitly.
      if (act.kind === "immediate-reset") setPendingAction(null);
    }
  };

  const filtered = users.filter((u) => {
    if (!search) return true;
    const s = search.toLowerCase();
    return u.username.toLowerCase().includes(s)
      || (u.nickname || "").toLowerCase().includes(s);
  });

  useBeforeUnload((isDirty && !submitting) || !!generatedCredential);

  // The lifecycle actions are gated on:
  //  - must NOT act on the owner role (would lock the only path to
  //    re-grant owner).
  //  - must NOT act on the current logged-in user (use the
  //    profile / logout flow for yourself).
  //  - must be admin or owner (everyone who can see this page is,
  //    but we encode the check explicitly so future role-tightening
  //    only touches one helper).
  const canActOnUser = (u: User) =>
    u.role !== "owner" && (currentUser ? u.username !== currentUser.username : true);

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
                        {u.must_reset_password && (
                          <span
                            className="admin-badge admin-badge-warning"
                            title="该用户已被标记强制重置密码"
                            style={{ marginLeft: 4 }}
                          >
                            等待重置
                          </span>
                        )}
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
                        {/* 强制重置密码 — when the user is in the
                            must_reset_password state, the button flips
                            to a disabled confirmation chip so the admin
                            knows the request has been sent. The chip
                            is the source of truth: the server is the
                            only one who can clear the flag (which
                            happens automatically on the user's next
                            successful login). */}
                        {u.must_reset_password ? (
                          <button
                            type="button"
                            className="admin-btn admin-btn-outline admin-btn-sm"
                            disabled
                            title="已标记。用户下次登录时将自动生成新密码。"
                          >
                            等待用户再次登录
                          </button>
                        ) : (
                          <button
                            type="button"
                            className="admin-btn admin-btn-warning admin-btn-sm"
                            disabled={!canActOnUser(u)}
                            onClick={() => setPendingAction({ kind: "force-reset", username: u.username })}
                            title={canActOnUser(u) ? "强制重置密码（用户下次登录时自动生成新密码）" : "无法对该用户执行此操作"}
                          >
                            🔑 强制重置
                          </button>
                        )}
                        <button
                          type="button"
                          className="admin-btn admin-btn-danger admin-btn-sm"
                          disabled={!canActOnUser(u)}
                          onClick={() => setPendingAction({ kind: "immediate-reset", username: u.username })}
                          title={canActOnUser(u) ? "立即重置密码（旧密码立即失效，新密码将显示给你）" : "无法对该用户执行此操作"}
                        >
                          ⚡ 立即重置
                        </button>
                        <button
                          type="button"
                          className="admin-btn admin-btn-ghost admin-btn-sm"
                          disabled={!canActOnUser(u)}
                          onClick={() => setPendingAction({ kind: "force-logout", username: u.username })}
                          title={canActOnUser(u) ? "立即退出登录（不修改密码）" : "无法对该用户执行此操作"}
                        >
                          🚪 退出登录
                        </button>
                        <button
                          className="admin-btn admin-btn-danger admin-btn-sm"
                          onClick={() => handleDelete(u.username)}
                          disabled={!canActOnUser(u)}
                          title={canActOnUser(u) ? "删除" : "无法删除该用户"}
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

      {/* Delete confirm — unchanged behaviour. */}
      <AdminConfirm
        open={!!pendingDelete}
        title="删除用户"
        message={pendingDelete ? `确定要删除用户「${pendingDelete}」吗？此操作不可撤销。` : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />

      {/* Lifecycle action confirm. The same AdminConfirm is reused for
          three different actions; the title/message/variant are picked
          from `pendingAction.kind` so the modal feels specific. */}
      <AdminConfirm
        open={!!pendingAction}
        title={
          pendingAction?.kind === "force-reset" ? "强制重置密码"
          : pendingAction?.kind === "immediate-reset" ? "立即重置密码"
          : pendingAction?.kind === "force-logout" ? "立即退出登录"
          : ""
        }
        message={
          pendingAction?.kind === "force-reset"
            ? `标记「${pendingAction.username}」为强制重置？用户当前会话会被踢出；用户下次登录时将自动生成新密码，弹窗只对用户可见。`
            : pendingAction?.kind === "immediate-reset"
            ? `立即重置「${pendingAction.username}」的密码？旧密码立即失效，新密码将在弹窗中显示给你（只有你看到），请自行转交给用户。`
            : pendingAction?.kind === "force-logout"
            ? `立即让「${pendingAction.username}」退出登录？密码不变，下一次请求该用户的会话将被拒绝。`
            : ""
        }
        confirmText={
          pendingAction?.kind === "force-reset" ? "标记强制重置"
          : pendingAction?.kind === "immediate-reset" ? "立即重置"
          : pendingAction?.kind === "force-logout" ? "强制退出"
          : "确定"
        }
        variant={pendingAction?.kind === "force-logout" ? "primary" : "danger"}
        onConfirm={confirmAction}
        onCancel={() => setPendingAction(null)}
      />

      {/* Sticky "your new password" modal — only used by immediate-reset.
          Persistent (no Escape / backdrop close) so the admin can't
          accidentally dismiss it before copying the new password. The
          "我已保存" button is the only way out. */}
      <AdminModal
        open={!!revealedPassword}
        onClose={() => undefined}
        title="新密码已生成"
        size="sm"
        persistent
        footer={
          <>
            <button
              type="button"
              className="admin-btn admin-btn-outline"
              onClick={copyRevealedPassword}
            >
              📋 复制
            </button>
            <button
              type="button"
              className="admin-btn admin-btn-primary"
              onClick={() => setRevealedPassword(null)}
            >
              我已保存
            </button>
          </>
        }
      >
        {revealedPassword && (
          <div>
            <p style={{ margin: "0 0 12px", lineHeight: 1.6 }}>
              「<strong>{revealedPassword.username}</strong>」的旧密码已立即失效。
              新密码只显示一次，请现在复制并通过安全的渠道转交给该用户。
            </p>
            <div className="admin-generated-password">
              <code style={{ wordBreak: "break-all" }}>{revealedPassword.password}</code>
            </div>
            <div className="admin-form-hint" style={{ marginTop: 12 }}>
              关闭此弹窗后密码将无法再次查看。如遗失，请再次执行「立即重置」重新生成。
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}
