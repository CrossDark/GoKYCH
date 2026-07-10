"use client";

import { useState, useEffect, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { getCsrf, login, apiUrl, apiFetch } from "@/lib/api";
import { supportsWebAuthn, arrayBufferToBase64Url, base64UrlToArrayBuffer } from "@/lib/webauthn";

async function loginWithPasskey(csrfToken: string) {
  const begin = await apiFetch(apiUrl("/api/auth/passkey/login/begin"), {
    method: "POST",
    headers: { "X-CSRF-Token": csrfToken },
  });
  if (!begin.ok) {
    const err = await begin.json().catch(() => ({}));
    throw new Error(err.error || "无法开始 Passkey 登录。");
  }
  const { publicKey: pk }: { publicKey: any } = await begin.json();
  if (!pk || !pk.challenge) throw new Error("服务器返回的登录选项格式不正确。");

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
  const finish = await apiFetch(apiUrl("/api/auth/passkey/login/finish"), {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
    body: JSON.stringify({ credential: responsePayload }),
  });
  if (!finish.ok) {
    const err = await finish.json().catch(() => ({}));
    throw new Error(err.error || "Passkey 验证失败。");
  }
  return await finish.json();
}

// parseMatrixQuestion parses a matrix-mode captcha question string of the
// shape "[[a,b],[c,d]] × [[e,f],[g,h]] = ?" into {A, B} for rendering.
// Returns null if the string doesn't match — callers fall back to plain text.
function parseMatrixQuestion(q: string): { A: number[][]; B: number[][] } | null {
  const m = q.match(
    /\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]\s*×\s*\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]/,
  );
  if (!m) return null;
  return {
    A: [[+m[1], +m[2]], [+m[3], +m[4]]],
    B: [[+m[5], +m[6]], [+m[7], +m[8]]],
  };
}

// MatrixSide renders one 2×2 matrix with bracket characters and tabular
// alignment. Used by both the question (with real values) and the answer
// cell layout. `body` is a render-prop so the question can pass <span>s and
// the input form can pass <input>s in the same shape. `variant` toggles
// "matrix-display" (read-only, for the question) vs "matrix-input" (with
// input cells) — they share bracket/body styles but differ in the cell
// styling block.
function MatrixSide({
  body,
  ariaLabel,
  variant = "display",
}: {
  body: (row: number, col: number) => React.ReactNode;
  ariaLabel: string;
  variant?: "display" | "input";
}) {
  const cls = variant === "input" ? "matrix-side matrix-input" : "matrix-side matrix-display";
  return (
    <span className={cls} aria-label={ariaLabel} role="group">
      <span className="bracket-col" aria-hidden="true">
        <span>⎡</span>
        <span>⎣</span>
      </span>
      <span className="body">
        {body(0, 0)}{body(0, 1)}{body(1, 0)}{body(1, 1)}
      </span>
      <span className="bracket-col" aria-hidden="true">
        <span>⎤</span>
        <span>⎦</span>
      </span>
    </span>
  );
}

export default function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const next = searchParams.get("next") || "/admin";

  const [csrfToken, setCsrfToken] = useState("");
  const [captchaQuestion, setCaptchaQuestion] = useState("");
  const [captchaMode, setCaptchaMode] = useState<"math" | "matrix">("math");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  // matrix mode: 2×2 grid of cells; math mode: [captcha] unused.
  const [captchaCells, setCaptchaCells] = useState<string[][]>([["", ""], ["", ""]]);
  const [captchaMath, setCaptchaMath] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const [showPasskey, setShowPasskey] = useState(false);

  const cellRefs = useRef<(HTMLInputElement | null)[][]>([[null, null], [null, null]]);

  const refreshCsrf = async () => {
    try {
      const resp = await getCsrf();
      setCsrfToken(resp.csrf_token);
      setCaptchaQuestion(resp.captcha.question);
      // Older servers don't echo `mode`; default to "math" so the input
      // hint stays consistent with the question format.
      const mode: "math" | "matrix" = resp.captcha.mode === "matrix" ? "matrix" : "math";
      setCaptchaMode(mode);
      // reset both inputs — captcha envelope is single-use anyway
      setCaptchaCells([["", ""], ["", ""]]);
      setCaptchaMath("");
    } catch {
      setError("无法连接服务器。");
    }
  };

  useEffect(() => {
    refreshCsrf();
    if (supportsWebAuthn()) {
      setShowPasskey(true);
    }
  }, []);

  const matrixQ = captchaMode === "matrix" ? parseMatrixQuestion(captchaQuestion) : null;

  // serializeMatrixAnswer converts the 2×2 grid into the JSON shape the
  // server's verifyMatrixAnswer expects: "[[a,b],[c,d]]". Empty or invalid
  // cells are left as "" — handleMatrixValidation reports them.
  const serializeMatrixAnswer = (cells: string[][]): string => {
    return `[[${cells[0][0]},${cells[0][1]}],[${cells[1][0]},${cells[1][1]}]]`;
  };

  const isCellValid = (v: string) => v === "" || /^-?\d+$/.test(v);

  const matrixComplete = captchaCells.every((row) => row.every((c) => /^-?\d+$/.test(c)));

  const handleCellChange = (r: number, c: number, v: string) => {
    // Allow only digits and an optional leading minus; clamp length so a
    // pasted "12345" doesn't blow out the cell visually. We deliberately do
    // NOT auto-advance to the next cell on type — for multi-digit answers
    // (e.g. "47" or "-87") that would race with the user still typing and
    // corrupt the input. Navigation is by Tab / arrow keys (handled in
    // handleCellKeyDown) or by clicking the destination cell.
    const cleaned = v.replace(/[^\d-]/g, "").replace(/(?!^)-/g, "").slice(0, 4);
    setCaptchaCells((prev) => {
      const next = prev.map((row) => row.slice());
      next[r][c] = cleaned;
      return next;
    });
  };

  const handleCellKeyDown = (r: number, c: number, e: React.KeyboardEvent<HTMLInputElement>) => {
    // Arrow keys to move around the grid (Excel-like); Backspace on empty
    // cell jumps to the previous one.
    const order: Array<[number, number]> = [[0, 0], [0, 1], [1, 0], [1, 1]];
    const idx = order.findIndex(([rr, cc]) => rr === r && cc === c);
    const move = (dr: number, dc: number) => {
      const nr = r + dr;
      const nc = c + dc;
      if (nr >= 0 && nr < 2 && nc >= 0 && nc < 2) {
        e.preventDefault();
        cellRefs.current[nr][nc]?.focus();
        cellRefs.current[nr][nc]?.select();
      }
    };
    switch (e.key) {
      case "ArrowRight": move(0, 1); break;
      case "ArrowLeft":  move(0, -1); break;
      case "ArrowDown":  move(1, 0); break;
      case "ArrowUp":    move(-1, 0); break;
      case "Backspace":
        if (!captchaCells[r][c] && idx > 0) {
          e.preventDefault();
          const [pr, pc] = order[idx - 1];
          cellRefs.current[pr][pc]?.focus();
        }
        break;
      case "Enter":
        // Submit on Enter from the last cell.
        if (r === 1 && c === 1) {
          e.preventDefault();
          (e.currentTarget.form as HTMLFormElement | null)?.requestSubmit();
        }
        break;
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (!username || !password) {
      setError("用户名和密码不能为空。");
      return;
    }
    let captcha: string;
    if (captchaMode === "matrix") {
      if (!matrixComplete) {
        setError("请填写完整的 2×2 矩阵答案（每格一个整数，可为负数）。");
        return;
      }
      captcha = serializeMatrixAnswer(captchaCells);
    } else {
      if (!captchaMath) {
        setError("请输入验证码答案。");
        return;
      }
      captcha = captchaMath;
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
    setPasskeyLoading(true);
    try {
      const csrf = await getCsrf();
      setCsrfToken(csrf.csrf_token);
      const resp = await loginWithPasskey(csrf.csrf_token);
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

        <div className="form-label">
          <span>验证码</span>
          {captchaMode === "matrix" && matrixQ ? (
            <div className="matrix-eq" data-testid="matrix-question" data-question={captchaQuestion}>
              <MatrixSide
                variant="display"
                ariaLabel="矩阵 A"
                body={(r, c) => <span key={`a-${r}-${c}`}>{matrixQ.A[r][c]}</span>}
              />
              <span className="matrix-op" aria-hidden="true">×</span>
              <MatrixSide
                variant="display"
                ariaLabel="矩阵 B"
                body={(r, c) => <span key={`b-${r}-${c}`}>{matrixQ.B[r][c]}</span>}
              />
              <span className="equals" aria-hidden="true">=</span>
              <span className="qmark" aria-hidden="true">?</span>
            </div>
          ) : (
            <span className="captcha-question">{captchaQuestion}</span>
          )}

          {captchaMode === "matrix" ? (
            <>
              <MatrixSide
                variant="input"
                ariaLabel="答案矩阵输入"
                body={(r, c) => (
                  <input
                    key={`cell-${r}-${c}`}
                    ref={(el) => { cellRefs.current[r][c] = el; }}
                    type="text"
                    inputMode="numeric"
                    className={`cell${!isCellValid(captchaCells[r][c]) ? " invalid" : ""}`}
                    value={captchaCells[r][c]}
                    onChange={(e) => handleCellChange(r, c, e.target.value)}
                    onKeyDown={(e) => handleCellKeyDown(r, c, e)}
                    aria-label={`第${r + 1}行第${c + 1}列`}
                    autoComplete="off"
                    spellCheck={false}
                    maxLength={4}
                  />
                )}
              />
              <div className="captcha-hint">
                请计算 A × B，将 2×2 结果矩阵填入上方格子。支持负数，使用 Tab / 方向键移动焦点。
              </div>
            </>
          ) : (
            <input
              type="text"
              value={captchaMath}
              onChange={(e) => setCaptchaMath(e.target.value)}
              placeholder="请输入答案"
              inputMode="numeric"
              required
            />
          )}
        </div>

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