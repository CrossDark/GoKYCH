"use client";

import { useState, useEffect } from "react";
import { getCsrf, getProfile, updateProfile } from "@/lib/api";
import type { User } from "@/lib/types";

export default function AdminProfile() {
  const [csrf, setCsrf] = useState("");
  const [user, setUser] = useState<User | null>(null);
  const [msg, setMsg] = useState("");
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
    setMsg("");
    try {
      const updated = await updateProfile(csrf, {
        nickname: form.nickname || undefined,
        bio: form.bio || undefined,
        avatar: form.avatar || undefined,
      });
      setUser(updated);
      setMsg("资料已更新。");
    } catch (err: any) {
      setMsg(err.message || "保存失败。");
    }
  };

  if (!user) return <div className="admin-profile"><p>加载中…</p></div>;

  return (
    <div className="admin-profile">
      <h1>个人资料</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      <form onSubmit={handleSave} className="admin-form">
        <div className="admin-profile-info">
          <p><strong>用户名：</strong>{user.username}</p>
          <p><strong>角色：</strong>{user.role}</p>
          <p><strong>注册时间：</strong>{new Date(user.created_at).toLocaleDateString("zh-CN")}</p>
        </div>

        <label>昵称
          <input value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} />
        </label>
        <label>简介
          <textarea value={form.bio} onChange={(e) => setForm({ ...form, bio: e.target.value })} rows={3} />
        </label>
        <label>头像 URL
          <input value={form.avatar} onChange={(e) => setForm({ ...form, avatar: e.target.value })} placeholder="https://..." />
        </label>
        <button type="submit" className="btn btn-primary">保存</button>
      </form>
    </div>
  );
}
