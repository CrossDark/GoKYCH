"use client";

import { useState, useEffect } from "react";
import { getCsrf } from "@/lib/api";
import { useToast } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";

interface Passkey {
  id: number;
  user_id: number;
  name: string;
  credential_id: string;
  transports: string[];
  sign_count: number;
  created_at: string;
}

// Helpers to convert ArrayBuffer <-> base64url (the wire format the
// WebAuthn API uses). Same as in the login page — duplicated here
// because pulling a third helper module for a 12-line function isn't
// worth the indirection.
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

export default function AdminPasskeys() {
  const [csrf, setCsrf] = useState("");
  const [keys, setKeys] = useState<Passkey[]>([]);
  const [loading, setLoading] = useState(true);
  const [registering, setRegistering] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<Passkey | null>(null);
  const toast = useToast();
  const [browserSupports, setBrowserSupports] = useState(false);

  useEffect(() => {
    setBrowserSupports(supportsWebAuthn());
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      loadKeys(r.csrf_token);
    });
  }, []);

  const loadKeys = (token: string) => {
    fetch("/api/auth/passkey", { headers: { "X-CSRF-Token": token } })
      .then((r) => r.json())
      .then((d) => { setKeys(d); setLoading(false); })
      .catch(() => setLoading(false));
  };

  const addPasskey = async () => {
    if (!browserSupports) {
      toast.error("当前浏览器不支持 WebAuthn。");
      return;
    }
    setRegistering(true);
    try {
      // 1. Begin registration.
      const begin = await fetch("/api/auth/passkey/register/begin", {
        method: "POST",
        headers: { "X-CSRF-Token": csrf },
      });
      if (!begin.ok) {
        const err = await begin.json().catch(() => ({}));
        throw new Error(err.error || "无法开始注册。");
      }
      const options = await begin.json();
      const challengeBuf = base64UrlToArrayBuffer(options.challenge);
      const userIdBuf = base64UrlToArrayBuffer(options.user.id);
      const excludeCreds = options.excludeCredentials?.map((c: any) => ({
        ...c,
        id: base64UrlToArrayBuffer(c.id),
      }));

      // 2. Prompt the authenticator. Browser shows the OS-level picker
      //    (Touch ID / Windows Hello / etc).
      const cred = await navigator.credentials.create({
        publicKey: {
          ...options,
          challenge: challengeBuf,
          user: { ...options.user, id: userIdBuf },
          excludeCredentials: excludeCreds,
        },
      } as CredentialCreationOptions) as PublicKeyCredential | null;
      if (!cred) throw new Error("已取消。");

      // 3. Ask the user to label this passkey for the list view.
      const name = (window.prompt("给这个 Passkey 起个名字（例如 MacBook Touch ID / iPhone 15）", "我的设备") || "").trim() || "未命名 Passkey";

      // 4. Send the response back.
      const att = cred.response as AuthenticatorAttestationResponse;
      const responsePayload = {
        id: cred.id,
        rawId: arrayBufferToBase64Url(cred.rawId),
        type: cred.type,
        response: {
          clientDataJSON: arrayBufferToBase64Url(att.clientDataJSON),
          attestationObject: arrayBufferToBase64Url(att.attestationObject),
          transports: att.getTransports?.() || [],
        },
        clientExtensionResults: cred.getClientExtensionResults?.() || {},
      };
      const finish = await fetch("/api/auth/passkey/register/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify({ name, credential: responsePayload }),
      });
      if (!finish.ok) {
        const err = await finish.json().catch(() => ({}));
        throw new Error(err.error || "注册失败。");
      }
      toast.success(`已添加「${name}」。`);
      loadKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "注册失败。");
    } finally {
      setRegistering(false);
    }
  };

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    try {
      const res = await fetch(`/api/auth/passkey/${pendingDelete.id}`, {
        method: "DELETE",
        headers: { "X-CSRF-Token": csrf },
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.error || "删除失败。");
      }
      toast.success(`已撤销「${pendingDelete.name}」。`);
      loadKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
    } finally {
      setPendingDelete(null);
    }
  };

  if (loading) return <div className="admin-page"><p>加载中…</p></div>;

  return (
    <div className="admin-page admin-passkeys">
      <div className="admin-page-header">
        <div>
          <h1>🔑 Passkey</h1>
          <div className="admin-page-subtitle">
            使用 Touch ID / Windows Hello / Android 生物识别 / 硬件密钥登录。
            同一账号可注册多个 Passkey。
          </div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>➕ 新增 Passkey</h2>
        </div>
        <div className="admin-card-body">
          {!browserSupports ? (
            <div className="admin-empty">
              当前浏览器不支持 WebAuthn。请用最新版 Chrome / Edge / Safari / Firefox。
            </div>
          ) : (
            <>
              <p style={{ marginBottom: 12, color: "var(--text-secondary)" }}>
                点击下方按钮，浏览器会弹出系统身份验证器（指纹 / 面容 / PIN）。
                验证通过后即注册成功。
              </p>
              <button
                className="admin-btn admin-btn-primary"
                onClick={addPasskey}
                disabled={registering}
              >
                {registering ? "等待验证…" : "添加 Passkey"}
              </button>
            </>
          )}
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>📋 已注册的 Passkey</h2>
        </div>
        <div className="admin-card-body no-padding">
          {keys.length === 0 ? (
            <div className="admin-empty">还没有 Passkey</div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>凭据 ID</th>
                  <th>传输方式</th>
                  <th className="col-date">注册时间</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id}>
                    <td>{k.name}</td>
                    <td><code style={{ fontSize: "0.75rem" }}>{k.credential_id.slice(0, 16)}…</code></td>
                    <td>
                      {k.transports.length > 0
                        ? k.transports.map((t) => <span key={t} className="admin-tag" style={{ marginRight: 4 }}>{t}</span>)
                        : "—"}
                    </td>
                    <td className="col-date">{new Date(k.created_at).toLocaleString("zh-CN")}</td>
                    <td className="col-actions">
                      <button
                        className="admin-btn admin-btn-danger admin-btn-sm"
                        onClick={() => setPendingDelete(k)}
                      >🗑 撤销</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-body" style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>
          <strong>说明：</strong>
          <ul style={{ paddingLeft: 20, marginTop: 6 }}>
            <li>注册 Passkey 后，<strong>密码登录将被禁用</strong>（站长 owner 账号除外，避免锁死）。</li>
            <li>同一账号可注册多个 Passkey（手机 + 电脑 + 硬件 key）。</li>
            <li>丢失所有 Passkey 时，请联系站长从数据库重置 <code>webauthn_credentials</code>。</li>
          </ul>
        </div>
      </div>

      <AdminModal
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        title="撤销 Passkey"
        size="sm"
      >
        {pendingDelete && (
          <div>
            <p>确定要撤销「<strong>{pendingDelete.name}</strong>」？</p>
            <p style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>
              撤销后该设备无法再用此 Passkey 登录。确保至少还有一个可用的登录方式。
            </p>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button className="admin-btn admin-btn-ghost" onClick={() => setPendingDelete(null)}>取消</button>
              <button className="admin-btn admin-btn-danger" onClick={confirmDelete}>🗑 撤销</button>
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}
