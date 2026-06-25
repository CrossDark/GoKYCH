"use client";

import { useState, useEffect, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { getCsrf, login } from "@/lib/api";

// Returns true if the browser can run WebAuthn (Touch ID / Windows Hello
// / Android biometrics / hardware key). On non-supporting browsers we
// hide the passkey button entirely rather than let the user click and
// see a "navigator.credentials is undefined" error.
function supportsWebAuthn() {
  if (typeof window === "undefined") return false;
  const w = window as any;
  return !!(w.PublicKeyCredential && typeof w.PublicKeyCredential === "function");
}

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

async function loginWithPasskey() {
  // 1. Get options from server.
  const begin = await fetch("/api/auth/passkey/login/begin", {
    method: "POST",
    headers: { "X-CSRF-Token": getCsrfToken() },
  });
  if (!begin.ok) {
    const err = await begin.json().catch(() => ({}));
    throw new Error(err.error || "无法开始 Passkey 登录。");
  }
  // go-webauthn returns protocol.CredentialAssertion = { publicKey: {...} }.
  // Unpack publicKey once then lift the ArrayBuffer-shaped fields out of it.
  const { publicKey: pk }: { publicKey: any } = await begin.json();
  if (!pk || !pk.challenge) throw new Error("服务器返回的登录选项格式不正确。");

  // 2. Hand to the authenticator.
  const challengeBuf = base64UrlToArrayBuffer(pk.challenge);
  const allowCreds = pk.allowCredentials?.map((c: any) => ({
    ...c,
    id: base64UrlToArrayBuffer(c.id),
  }));
  const cred = await navigator.credentials.get({
    publicKey: {
      ...pk,
      challenge: challengeBuf,
      allowCredentials: allowCreds,
    },
  } as CredentialRequestOptions) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey 登录已取消。");

  // 3. Send the response back. The server only needs the raw JSON shape
  //    the spec mandates; it re-parses from the request body.
  const responsePayload = {
    id: cred.id,
    rawId: arrayBufferToBase64Url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: arrayBufferToBase64Url((cred.response as AuthenticatorAssertionResponse).clientDataJSON),
      authenticatorData: arrayBufferToBase64Url((cred.response as AuthenticatorAssertionResponse).authenticatorData),
      signature: arrayBufferToBase64Url((cred.response as AuthenticatorAssertionResponse).signature),
      userHandle: (cred.response as AuthenticatorAssertionResponse).userHandle
        ? arrayBufferToBase64Url((cred.response as AuthenticatorAssertionResponse).userHandle!)
        : null,
    },
  };
  const finish = await fetch("/api/auth/passkey/login/finish", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": getCsrfToken() },
    body: JSON.stringify({ credential: responsePayload }),
  });
  if (!finish.ok) {
    const err = await finish.json().catch(() => ({}));
    throw new Error(err.error || "Passkey 验证失败。");
  }
  return await finish.json();
}

let _csrfToken = "";
function getCsrfToken() { return _csrfToken; }
function setCsrfToken(t: string) { _csrfToken = t; }

function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const next = searchParams.get("next") || "/admin";

  const [csrfToken, setCsrfTokenState] = useState("");
  const [captchaQuestion, setCaptchaQuestion] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [captcha, setCaptcha] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const [showPasskey, setShowPasskey] = useState(false);

  useEffect(() => { setShowPasskey(supportsWebAuthn()); }, []);

  const refreshCsrf = async () => {
    try {
      const resp = await getCsrf();
      setCsrfToken(resp.csrf_token);
      setCsrfTokenState(resp.csrf_token);
      setCaptchaQuestion(resp.captcha.question);
    } catch {
      setError("无法连接服务器。");
    }
  };

  useEffect(() => {
    refreshCsrf();
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (!username || !password) {
      setError("用户名和密码不能为空。");
      return;
    }
    if (!captcha) {
      setError("请输入验证码答案。");
      return;
    }
    setLoading(true);
    try {
      const resp = await login({
        username,
        password,
        captcha,
        csrf_token: csrfToken,
      });
      router.push(resp.next || next);
    } catch (err: any) {
      setError(err.message || "登录失败。");
      await refreshCsrf();
    } finally {
      setLoading(false);
    }
  };

  const handlePasskeyLogin = async () => {
    setError("");
    setPasskeyLoading(true);
    try {
      // Make sure we have a fresh CSRF token in the global slot the
      // loginWithPasskey helper reads. (We can't pass it down cleanly
      // through the helper without changing the signature, and the
      // helper is also exported for symmetry with the registration
      // flow which does the same.)
      const csrf = await getCsrf();
      setCsrfToken(csrf.csrf_token);
      setCsrfTokenState(csrf.csrf_token);
      const resp = await loginWithPasskey();
      router.push(resp.next || next);
    } catch (err: any) {
      setError(err.message || "Passkey 登录失败。");
    } finally {
      setPasskeyLoading(false);
    }
  };

  return (
    <div className="page login-page">
      <h1>登录</h1>
      {error && <div className="form-error">{error}</div>}

      {showPasskey && (
        <div className="passkey-section">
          <button
            type="button"
            className="btn btn-secondary btn-block"
            onClick={handlePasskeyLogin}
            disabled={passkeyLoading}
          >
            {passkeyLoading ? "验证中…" : "🔑 使用 Passkey 登录"}
          </button>
          <div className="login-divider"><span>或使用密码</span></div>
        </div>
      )}

      <form className="login-form" onSubmit={handleSubmit}>
        <label className="form-label">
          用户名
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="请输入用户名"
            autoComplete="username"
            required={!showPasskey}
          />
        </label>

        <label className="form-label">
          密码
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="请输入密码"
            autoComplete="current-password"
            required={!showPasskey}
          />
        </label>

        <label className="form-label">
          验证码
          <span className="captcha-question">{captchaQuestion}</span>
          <input
            type="text"
            value={captcha}
            onChange={(e) => setCaptcha(e.target.value)}
            placeholder="请输入答案"
            inputMode="numeric"
            required={!showPasskey}
          />
        </label>

        <button
          type="submit"
          className="btn btn-primary btn-block"
          disabled={loading}
        >
          {loading ? "登录中…" : "登录"}
        </button>
      </form>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={<div className="page login-page"><h1>登录</h1><p>加载中…</p></div>}>
      <LoginForm />
    </Suspense>
  );
}
