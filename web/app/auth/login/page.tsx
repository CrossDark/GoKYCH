"use client";

import { useState, useEffect, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { getCsrf, login } from "@/lib/api";

function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const next = searchParams.get("next") || "/admin";

  const [csrfToken, setCsrfToken] = useState("");
  const [captchaQuestion, setCaptchaQuestion] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [captcha, setCaptcha] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const refreshCsrf = async () => {
    try {
      const resp = await getCsrf();
      setCsrfToken(resp.csrf_token);
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

  return (
    <div className="page login-page">
      <h1>登录</h1>
      <form className="login-form" onSubmit={handleSubmit}>
        {error && <div className="form-error">{error}</div>}

        <label className="form-label">
          用户名
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="请输入用户名"
            autoComplete="username"
            required
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
            required
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
            required
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
