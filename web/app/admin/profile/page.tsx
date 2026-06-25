"use client";

import { useState, useEffect } from "react";
import { getCsrf, getProfile, updateProfile, changeMyPassword } from "@/lib/api";
import type { User } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";

// ── WebAuthn helpers (duplicated from the login page; kept local so this
//    page stays self-contained) ─────────────────────────────────────────
function arrayBufferToBase64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
function base64UrlToArrayBuffer(s: string): ArrayBuffer {
  const padded = s.replace(/-/g, "+").replace(/_/g, "/") + "==".slice(0, (4 - (s.length % 4)) % 4);
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}
function supportsWebAuthn() {
  if (typeof window === "undefined") return false;
  return !!(window as any).PublicKeyCredential;
}

interface MyPasskey {
  id: number;
  name: string;
  credential_id: string;
  transports: string[];
  sign_count: number;
  created_at: string;
}

export default function AdminProfile() {
  const [csrf, setCsrf] = useState("");
  const [user, setUser] = useState<User | null>(null);
  const toast = useToast();
  const [form, setForm] = useState({ nickname: "", bio: "", avatar: "" });
  const [initial, setInitial] = useState({ nickname: "", bio: "", avatar: "" });
  const [submitting, setSubmitting] = useState(false);
  const isProfileDirty = form.nickname !== initial.nickname
    || form.bio !== initial.bio
    || form.avatar !== initial.avatar;

  // ── Password change ──
  const [pw, setPw] = useState({ old: "", next: "", confirm: "" });
  const [pwSubmitting, setPwSubmitting] = useState(false);

  // ── My passkeys ──
  const [myKeys, setMyKeys] = useState<MyPasskey[]>([]);
  const [myKeysLoading, setMyKeysLoading] = useState(true);
  const [registering, setRegistering] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<MyPasskey | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [browserSupports, setBrowserSupports] = useState(false);

  useEffect(() => {
    setBrowserSupports(supportsWebAuthn());
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getProfile(r.csrf_token).then((u) => {
        setUser(u);
        const init = { nickname: u.nickname || "", bio: u.bio || "", avatar: u.avatar || "" };
        setForm(init);
        setInitial(init);
      }).catch(() => {});
      loadMyKeys(r.csrf_token);
    });
  }, []);

  // Out-of-tab sync: if the user just set a passkey and would be locked out
  // of password login, this banner explains it (owner exempt — but we
  // still show a soft hint for them).
  useBeforeUnload(isProfileDirty && !submitting);

  const loadMyKeys = (token: string) => {
    setMyKeysLoading(true);
    fetch("/api/auth/passkey", { headers: { "X-CSRF-Token": token } })
      .then((r) => r.json())
      .then((d: MyPasskey[]) => { setMyKeys(d || []); setMyKeysLoading(false); })
      .catch(() => setMyKeysLoading(false));
  };

  // ── Save profile ──
  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      const updated = await updateProfile(csrf, {
        nickname: form.nickname || undefined,
        bio: form.bio || undefined,
        avatar: form.avatar || undefined,
      });
      setUser(updated);
      const init = { nickname: updated.nickname || "", bio: updated.bio || "", avatar: updated.avatar || "" };
      setInitial(init);
      setForm(init);
      toast.success("资料已更新。");
    } catch (err: any) {
      toast.error(err.message || "保存失败。");
    } finally {
      setSubmitting(false);
    }
  };

  // ── Change password ──
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

  // ── Register a passkey for myself ──
  const addPasskey = async () => {
    if (!browserSupports) { toast.error("当前浏览器不支持 WebAuthn。"); return; }
    setRegistering(true);
    try {
      const begin = await fetch("/api/auth/passkey/register/begin", {
        method: "POST", headers: { "X-CSRF-Token": csrf },
      });
      if (!begin.ok) { const e = await begin.json().catch(() => ({})); throw new Error(e.error || "无法开始注册。"); }
      // go-webauthn returns protocol.CredentialCreation = { publicKey: {...} }.
      // Unpack publicKey once then lift ArrayBuffer-shaped fields out of it.
      const { publicKey: pk }: { publicKey: any } = await begin.json();
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
      const finish = await fetch("/api/auth/passkey/register/finish", {
        method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify({ name, credential: payload }),
      });
      if (!finish.ok) { const e = await finish.json().catch(() => ({})); throw new Error(e.error || "注册失败。"); }
      toast.success(`已添加「${name}」。`);
      loadMyKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "注册失败。");
    } finally {
      setRegistering(false);
    }
  };

  const deleteMyPasskey = async () => {
    if (!pendingDelete) return;
    setDeletingId(pendingDelete.id);
    try {
      const res = await fetch(`/api/auth/passkey/${pendingDelete.id}`, { method: "DELETE", headers: { "X-CSRF-Token": csrf } });
      if (!res.ok) { const e = await res.json().catch(() => ({})); throw new Error(e.error || "删除失败。"); }
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

  // Non-owner with at least one passkey → password login disabled self-help hint.
  const passwordLoginDisabled = user.role !== "owner" && myKeys.length > 0;

  return (
    <div className="admin-profile">
      <div className="admin-page-header">
        <div>
          <h1>个人资料</h1>
          <div className="admin-page-subtitle">资料 · 密码 · Passkey</div>
        </div>
      </div>

      {/* Account info (read-only) */}
      <div className="admin-card">
        <div className="admin-card-header"><h2>👤 账号信息</h2></div>
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

      {/* Edit profile */}
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
            <div className="admin-form-actions">
              <button type="submit" className={`admin-btn admin-btn-primary ${submitting ? "admin-btn-loading" : ""}`} disabled={submitting || !isProfileDirty}>保存修改</button>
            </div>
          </div>
        </div>
      </form>

      {/* Change password */}
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

      {/* My passkeys */}
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
                      <td className="col-date">{new Date(k.created_at).toLocaleString("zh-CN")}</td>
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

      {/* Delete-my-passkey confirm */}
      <AdminModal open={!!pendingDelete} onClose={() => setPendingDelete(null)} title="撤销我的 Passkey" size="sm">
        {pendingDelete && (
          <div>
            <p>确定要撤销「<strong>{pendingDelete.name}</strong>」？</p>
            <p style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>撤销后该设备无法再用此 Passkey 登录。请确保至少保留一种可用登录方式。</p>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button className="admin-btn admin-btn-ghost" onClick={() => setPendingDelete(null)}>取消</button>
              <button className="admin-btn admin-btn-danger" onClick={deleteMyPasskey} disabled={!!deletingId}>🗑 撤销</button>
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}