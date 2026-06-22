"use client";

import { useState, useEffect } from "react";
import { getCsrf, listUsers, createUser, updateUserRole, deleteUser } from "@/lib/api";
import type { User } from "@/lib/types";

export default function AdminUsers() {
  const [csrf, setCsrf] = useState("");
  const [users, setUsers] = useState<User[]>([]);
  const [msg, setMsg] = useState("");
  const [form, setForm] = useState({ username: "", password: "", nickname: "", role: "user" });

  const load = () => {
    if (!csrf) return;
    listUsers(csrf).then(setUsers).catch((e) => setMsg(e.message));
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      listUsers(r.csrf_token).then(setUsers).catch(() => {});
    });
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg("");
    try {
      await createUser(csrf, { username: form.username, password: form.password, nickname: form.nickname || undefined, role: form.role });
      setMsg("用户已创建。");
      setForm({ username: "", password: "", nickname: "", role: "user" });
      load();
    } catch (err: any) {
      setMsg(err.message || "创建失败。");
    }
  };

  const handleRoleChange = async (username: string, role: string) => {
    try {
      await updateUserRole(csrf, username, role);
      load();
    } catch (err: any) {
      setMsg(err.message || "操作失败。");
    }
  };

  const handleDelete = async (username: string) => {
    if (!confirm(`确定删除用户「${username}」吗？`)) return;
    try {
      await deleteUser(csrf, username);
      setMsg("用户已删除。");
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  return (
    <div className="admin-users">
      <h1>用户管理</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      <details className="admin-form-section">
        <summary>新建用户</summary>
        <form onSubmit={handleCreate} className="admin-form">
          <label>用户名 <input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} required /></label>
          <label>密码 <input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required /></label>
          <label>昵称 <input value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} /></label>
          <label>角色
            <select value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })}>
              <option value="user">user</option>
              <option value="admin">admin</option>
              <option value="owner">owner</option>
            </select>
          </label>
          <button type="submit" className="btn btn-primary">创建</button>
        </form>
      </details>

      {users.length === 0 ? (
        <p className="empty-message">暂无用户。</p>
      ) : (
        <table className="admin-table">
          <thead>
            <tr><th>ID</th><th>用户名</th><th>昵称</th><th>角色</th><th>创建时间</th><th>操作</th></tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>{u.id}</td>
                <td>{u.username}</td>
                <td>{u.nickname || "—"}</td>
                <td>
                  <select value={u.role} onChange={(e) => handleRoleChange(u.username, e.target.value)} className="admin-role-select">
                    <option value="user">user</option>
                    <option value="admin">admin</option>
                    <option value="owner">owner</option>
                  </select>
                </td>
                <td>{new Date(u.created_at).toLocaleDateString("zh-CN")}</td>
                <td>
                  <button className="btn btn-small btn-danger" onClick={() => handleDelete(u.username)}>删除</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
