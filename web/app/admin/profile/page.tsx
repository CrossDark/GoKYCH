"use client";

import { useState, useEffect } from "react";
import {
  getCsrf, getProfile, updateProfile, changeMyPassword,
  listMyPasskeys, beginPasskeyRegister, finishPasskeyRegister, deleteMyPasskey,
} from "@/lib/api";
import type { User, MyPasskeyInfo } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";
import { UserAvatar } from "@/components/admin/UserAvatar";
import { supportsWebAuthn, arrayBufferToBase64Url, base64UrlToArrayBuffer } from "@/lib/webauthn";
import { fmtDate, fmtDateTime } from "@/lib/format";

export default function AdminProfile() {
  const [csrf, setCsrf] = useState("");
  const [user, setUser] = useState<User | null>(null);
  const toast = useToast();
  const [form, setForm] = useState({
    nickname: "",
    bio: "",
    avatar: "",
    social_email: "",
    social_github: "",
    social_qq: "",
  });
  const [initial, setInitial] = useState({
    nickname: "",
    bio: "",
    avatar: "",
    social_email: "",
    social_github: "",
    social_qq: "",
  });
  const [submitting, setSubmitting] = useState(false);
  const isProfileDirty = form.nickname !== initial.nickname
    || form.bio !== initial.bio
    || form.avatar !== initial.avatar
    || form.social_email !== initial.social_email
    || form.social_github !== initial.social_github
    || form.social_qq !== initial.social_qq;

  const [pw, setPw] = useState({ old: "", next: "", confirm: "" });
  const [pwSubmitting, setPwSubmitting] = useState(false);

  const [myKeys, setMyKeys] = useState<MyPasskeyInfo[]>([]);
  const [myKeysLoading, setMyKeysLoading] = useState(true);
  const [registering, setRegistering] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<MyPasskeyInfo | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [browserSupports, setBrowserSupports] = useState(false);

  useEffect(() => {
    setBrowserSupports(supportsWebAuthn());
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getProfile(r.csrf_token).then((u) => {
        setUser(u);
        const init = {
          nickname: u.nickname || "",
          bio: u.bio || "",
          avatar: u.avatar || "",
          social_email: u.social_email || "",
          social_github: u.social_github || "",
          social_qq: u.social_qq || "",
        };
        setForm(init);
        setInitial(init);
      }).catch(() => {});
      loadMyKeys(r.csrf_token);
    });
  }, []);

  useBeforeUnload(isProfileDirty && !submitting);

  const loadMyKeys = async (token: string) => {
    setMyKeysLoading(true);
    try {
      const d = await listMyPasskeys(token);
      setMyKeys(d || []);
    } catch {
    } finally {
      setMyKeysLoading(false);
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      const updated = await updateProfile(csrf, {
        nickname: form.nickname || undefined,
        bio: form.bio || undefined,
        avatar: form.avatar || undefined,
        social_email: form.social_email || undefined,
        social_github: form.social_github || undefined,
        social_qq: form.social_qq || undefined,
      });
      setUser(updated);
      const init = {
        nickname: updated.nickname || "",
        bio: updated.bio || "",
        avatar: updated.avatar || "",
        social_email: updated.social_email || "",
        social_github: updated.social_github || "",
        social_qq: updated.social_qq || "",
      };
      setInitial(init);
      setForm(init);
      toast.success("资料已更新。");
    } catch (err: any) {
      toast.error(err.message || "保存失败。");
    } finally {
      setSubmitting(false);
    }
  };

  const pwValid = pw.next.length >= 8 && /[A-Z]/.test(pw.next) && /[a-z]/.test(pw.next) && /[0-9]/.test(pw.next) && !/\s/.test(pw.next);
  const handlePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pw.old || !pw.next) { toast.warning("旧密码和新密码不能为空。"); return; }
    if (!pwValid) { toast.warning("新密码需 8 位以上，含大小写字母和数字，无空格。"); return; }
    if (pw.next !== pw.confirm) { toast.warning("两次输入的新密码不一致。"); return; }
    if (pw.old === pw.next) { toast.warning("新密码不能与旧密码相同。"); return; }
    setPwSubmitting(true);
    try {
      await changeMyPassword(csrf, { old_password: pw.old, new_password: pw.next });
      setPw({ old: "", next: "", confirm: "" });
      toast.success("密码已修改。");
    } catch (err: any) {
      toast.error(err.message || "修改失败。");
    } finally {
      setPwSubmitting(false);
    }
  };

  const addPasskey = async () => {
    if (!browserSupports) { toast.error("当前浏览器不支持 WebAuthn。"); return; }
    setRegistering(true);
    try {
      const beginData = await beginPasskeyRegister(csrf);
      const { publicKey: pk } = beginData;
      if (!pk || !pk.challenge) throw new Error("服务器返回的注册选项格式不正确。");
      const challengeBuf = base64UrlToArrayBuffer(pk.challenge);
      const userIdBuf = base64UrlToArrayBuffer(pk.user?.id ?? "");
      const excludeCreds = pk.excludeCredentials?.map((c: any) => ({ ...c, id: base64UrlToArrayBuffer(c.id) }));
      const cred = await navigator.credentials.create({
        publicKey: { ...pk, challenge: challengeBuf, user: { ...pk.user, id: userIdBuf }, excludeCredentials: excludeCreds },
      } as CredentialCreationOptions) as PublicKeyCredential | null;
      if (!cred) throw new Error("已取消。");
      const name = (window.prompt("给这个 Passkey 起个名字（例如 MacBook Touch ID）", "我的设备") || "").trim() || "未命名 Passkey";
      const att = cred.response as AuthenticatorAttestationResponse;
      const payload = {
        id: cred.id, rawId: arrayBufferToBase64Url(cred.rawId), type: cred.type,
        response: {
          clientDataJSON: arrayBufferToBase64Url(att.clientDataJSON),
          attestationObject: arrayBufferToBase64Url(att.attestationObject),
          transports: att.getTransports?.() || [],
        },
        clientExtensionResults: cred.getClientExtensionResults?.() || {},
      };
      await finishPasskeyRegister(csrf, { name, credential: payload });
      toast.success(`已添加「${name}」。`);
      loadMyKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "注册失败。");
    } finally {
      setRegistering(false);
    }
  };

  const handleDeleteMyPasskey = async () => {
    if (!pendingDelete) return;
    setDeletingId(pendingDelete.id);
    try {
      await deleteMyPasskey(csrf, pendingDelete.id);
      toast.success(`已撤销「${pendingDelete.name}」。`);
      loadMyKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
    } finally {
      setDeletingId(null);
      setPendingDelete(null);
    }
  };

  if (!user) return <div className="admin-profile"><div className="admin-card"><div className="admin-card-body"><div className="admin-empty">加载中…</div></div></div></div>;

  const passwordLoginDisabled = user.role !== "owner" && myKeys.length > 0;

  return (
    <div className="admin-profile">
      <div className="admin-page-header">
        <div>
          <h1>个人资料</h1>
          <div className="admin-page-subtitle">资料 · 密码 · Passkey</div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header"><h2>👤 账号信息</h2></div>
        <div className="admin-card-body">
          <div style={{ display: "flex", alignItems: "center", gap: "1rem", flexWrap: "wrap" }}>
            <UserAvatar user={user} size={56} />
            <div style={{ flex: 1, minWidth: 200 }}>
              <div style={{ fontWeight: 600, fontSize: "1rem" }}>{user.nickname || user.username}</div>
              <div style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: 2 }}>
                @{user.username} · <span className={`admin-badge admin-badge-${user.role === "owner" ? "danger" : user.role === "admin" ? "warning" : "neutral"}`}>{user.role}</span>
              </div>
            </div>
            <div style={{ color: "var(--text-muted)", fontSize: "0.82rem" }}>
              注册于 {fmtDate(user.created_at)}
            </div>
          </div>
        </div>
      </div>

      <form onSubmit={handleSave} className="admin-card" style={{ marginTop: "1.25rem" }}>
        <div className="admin-card-header"><h2>✏️ 编辑资料</h2></div>
        <div className="admin-card-body">
          <div className="admin-form">
            <div className="admin-form-group">
              <label htmlFor="profile-nickname">昵称</label>
              <input id="profile-nickname" value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} placeholder="显示在评论 / 文章中的名字" />
            </div>
            <div className="admin-form-group">
              <label htmlFor="profile-bio">简介</label>
              <textarea id="profile-bio" value={form.bio} onChange={(e) => setForm({ ...form, bio: e.target.value })} rows={4} className="admin-textarea-plain" placeholder="一句话介绍自己…" />
            </div>
            <div className="admin-form-group">
              <label htmlFor="profile-avatar">头像 URL</label>
              <input id="profile-avatar" value={form.avatar} onChange={(e) => setForm({ ...form, avatar: e.target.value })} placeholder="https://... 或 /uploads/xxx" aria-describedby="profile-avatar-hint" />
              <div id="profile-avatar-hint" className="admin-form-hint">支持外链或站内上传文件</div>
            </div>

            <div className="admin-form-group" style={{ marginTop: 8 }}>
              <label style={{ marginBottom: 6 }}>🌐 社交媒体</label>
              <div style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "8px 12px", alignItems: "center" }}>
                <label htmlFor="profile-social-email" style={{ margin: 0 }}>邮箱</label>
                <input
                  id="profile-social-email"
                  type="email"
                  value={form.social_email}
                  onChange={(e) => setForm({ ...form, social_email: e.target.value })}
                  placeholder="you@example.com"
                />
                <label htmlFor="profile-social-github" style={{ margin: 0 }}>GitHub</label>
                <input
                  id="profile-social-github"
                  value={form.social_github}
                  onChange={(e) => setForm({ ...form, social_github: e.target.value })}
                  placeholder="https://github.com/yourname"
                />
                <label htmlFor="profile-social-qq" style={{ margin: 0 }}>QQ</label>
                <input
                  id="profile-social-qq"
                  value={form.social_qq}
                  onChange={(e) => setForm({ ...form, social_qq: e.target.value.replace(/\D/g, "") })}
                  placeholder="QQ 号（纯数字）"
                  inputMode="numeric"
                  maxLength={20}
                />
              </div>
              <div className="admin-form-hint">每个用户的社交链接独立保存；留空表示不公开。</div>
            </div>
            <div className="admin-form-actions">
              <button type="submit" className={`admin-btn admin-btn-primary ${submitting ? "admin-btn-loading" : ""}`} disabled={submitting || !isProfileDirty}>保存修改</button>
            </div>
          </div>
        </div>
      </form>

      <form onSubmit={handlePassword} className="admin-card" style={{ marginTop: "1.25rem" }}>
        <div className="admin-card-header"><h2>🔐 修改密码</h2></div>
        <div className="admin-card-body">
          {passwordLoginDisabled && (
            <div className="admin-notice admin-notice-warning" style={{ marginBottom: 12 }}>
              你已绑定 Passkey，密码登录已禁用。改密码不会影响当前的 Passkey 登录；若删除所有 Passkey，密码登录会自动恢复。
            </div>
          )}
          <div className="admin-form">
            <div className="admin-form-group">
              <label htmlFor="pw-old">旧密码</label>
              <input id="pw-old" type="password" value={pw.old} onChange={(e) => setPw({ ...pw, old: e.target.value })} autoComplete="current-password" placeholder="当前密码" />
            </div>
            <div className="admin-form-group">
              <label htmlFor="pw-new">新密码</label>
              <input id="pw-new" type="password" value={pw.next} onChange={(e) => setPw({ ...pw, next: e.target.value })} autoComplete="new-password" placeholder="8 位以上，含大小写字母和数字" aria-describedby="pw-new-hint" />
              <div id="pw-new-hint" className="admin-form-hint">8–72 位，必须包含大写字母、小写字母和数字，不含空格。</div>
            </div>
            <div className="admin-form-group">
              <label htmlFor="pw-confirm">确认新密码</label>
              <input id="pw-confirm" type="password" value={pw.confirm} onChange={(e) => setPw({ ...pw, confirm: e.target.value })} autoComplete="new-password" placeholder="再次输入新密码" />
            </div>
            <div className="admin-form-actions">
              <button type="submit" className={`admin-btn admin-btn-primary ${pwSubmitting ? "admin-btn-loading" : ""}`} disabled={pwSubmitting || !pw.old || !pw.next || !pw.confirm}>修改密码</button>
            </div>
          </div>
        </div>
      </form>

      <div className="admin-card" style={{ marginTop: "1.25rem" }}>
        <div className="admin-card-header"><h2>🔑 我的 Passkey</h2></div>
        <div className="admin-card-body">
          <p style={{ color: "var(--text-secondary)", marginBottom: 12 }}>
            用 Touch ID / Windows Hello / 安卓生物识别 / 硬件密钥免密登录。同一账号可登记多个。
            {passwordLoginDisabled ? "" : " 设置后密码登录将自动禁用（站长账号豁免）。"}
          </p>
          {!browserSupports ? (
            <div className="admin-empty">当前浏览器不支持 WebAuthn，请用最新版 Chrome / Edge / Safari / Firefox。</div>
          ) : (
            <button className="admin-btn admin-btn-primary" onClick={addPasskey} disabled={registering} style={{ marginBottom: 16 }}>
              {registering ? "等待验证…" : "➕ 添加 Passkey"}
            </button>
          )}
          <div className="no-padding">
            {myKeysLoading ? (
              <div className="admin-empty">加载中…</div>
            ) : myKeys.length === 0 ? (
              <div className="admin-empty">还没有 Passkey</div>
            ) : (
              <table className="admin-table">
                <thead>
                  <tr>
                    <th>名称</th><th>凭据 ID</th><th>传输方式</th>
                    <th className="col-date">注册时间</th><th className="col-actions">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {myKeys.map((k) => (
                    <tr key={k.id}>
                      <td>{k.name}</td>
                      <td><code style={{ fontSize: "0.75rem" }}>{k.credential_id.slice(0, 16)}…</code></td>
                      <td>{k.transports.length > 0 ? k.transports.map((t) => <span key={t} className="admin-tag" style={{ marginRight: 4 }}>{t}</span>) : "—"}</td>
                      <td className="col-date">{fmtDateTime(k.created_at)}</td>
                      <td className="col-actions">
                        <button className="admin-btn admin-btn-danger admin-btn-sm" onClick={() => setPendingDelete(k)} disabled={deletingId === k.id}>🗑 撤销</button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
</div>

      <AdminModal open={!!pendingDelete} onClose={() => setPendingDelete(null)} title="撤销我的 Passkey" size="sm">
        {pendingDelete && (
          <div>
            <p>确定要撤销「<strong>{pendingDelete.name}</strong>」？</p>
            <p style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>撤销后该设备无法再用此 Passkey 登录。请确保至少保留一种可用登录方式。</p>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button className="admin-btn admin-btn-ghost" onClick={() => setPendingDelete(null)}>取消</button>
              <button className="admin-btn admin-btn-danger" onClick={handleDeleteMyPasskey} disabled={!!deletingId}>🗑 撤销</button>
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}