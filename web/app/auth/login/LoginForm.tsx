"use client";

import { useState, useEffect, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import katex from "katex";
import "katex/dist/katex.min.css";
import { getCsrf, login, apiUrl, apiFetch } from "@/lib/api";
import { supportsWebAuthn, arrayBufferToBase64Url, base64UrlToArrayBuffer } from "@/lib/webauthn";
import { AdminModal } from "@/components/admin/AdminModal";

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

// renderMatrixLatex returns a KaTeX-rendered HTML string for the captcha
// question. Uses LaTeX's bmatrix environment so the brackets, alignment,
// and spacing match the typography of a textbook — \left[ \right] scaling,
// proper row spacing, operator spacing for ×. throwOnError:false so a
// malformed question degrades gracefully rather than 500ing the page.
function renderMatrixLatex(A: number[][], B: number[][]): string {
  const fmt = (m: number[][]) =>
    `\\begin{bmatrix} ${m[0][0]} & ${m[0][1]} \\\\ ${m[1][0]} & ${m[1][1]} \\end{bmatrix}`;
  const latex = `${fmt(A)} \\times ${fmt(B)} = \\ ?`;
  return katex.renderToString(latex, {
    throwOnError: false,
    displayMode: false,
    strict: "ignore",
  });
}

// MatrixInput is the 2×2 answer input. The structure is explicit (rows +
// row-separator), not implicit-grid, so a reader sees two rows of two
// cells rather than a single block. CSS-drawn vertical bars on the sides
// mimic LaTeX's \left[ \right] without depending on a font's bracket
// glyphs — they scale to whatever the input height turns out to be.
function MatrixInput({
  cells,
  invalid,
  refs,
  onChange,
  onKeyDown,
}: {
  cells: string[][];
  invalid: (v: string) => boolean;
  refs: React.MutableRefObject<(HTMLInputElement | null)[][]>;
  onChange: (r: number, c: number, v: string) => void;
  onKeyDown: (r: number, c: number, e: React.KeyboardEvent<HTMLInputElement>) => void;
}) {
  return (
    <span className="matrix-input" role="group" aria-label="答案矩阵输入">
      <span className="matrix-bracket matrix-bracket-left" aria-hidden="true" />
      <span className="cells">
        {[0, 1].map((r) => (
          <span className="row" key={`row-${r}`}>
            {[0, 1].map((c) => (
              <input
                key={`cell-${r}-${c}`}
                ref={(el) => { refs.current[r][c] = el; }}
                type="text"
                inputMode="numeric"
                className={`cell${invalid(cells[r][c]) ? " invalid" : ""}`}
                value={cells[r][c]}
                onChange={(e) => onChange(r, c, e.target.value)}
                onKeyDown={(e) => onKeyDown(r, c, e)}
                aria-label={`第${r + 1}行第${c + 1}列`}
                autoComplete="off"
                spellCheck={false}
                maxLength={4}
              />
            ))}
          </span>
        ))}
        {/* Horizontal separator between row 1 and row 2 — explicit so the
            shape of the matrix is unmistakable in the rendered UI, the
            same way \\ in LaTeX makes the second row clearly distinct. */}
        <span className="row-sep" aria-hidden="true" />
      </span>
      <span className="matrix-bracket matrix-bracket-right" aria-hidden="true" />
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
  // Force-reset-on-next-login: when the server returns new_password in
  // the login response, we stash it here and refuse to redirect until
  // the user has acknowledged (and ideally copied) the new credential.
  // The admin never sees this — the modal is the only place this
  // plaintext is ever displayed. `next` is captured at the same time
  // so we know where to send the user after they click "我已复制".
  const [forcedNewPassword, setForcedNewPassword] = useState<{ password: string; next: string } | null>(null);

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
    const timer = window.setTimeout(() => {
      void refreshCsrf();
      if (supportsWebAuthn()) {
        setShowPasskey(true);
      }
    }, 0);
    return () => window.clearTimeout(timer);
  }, []);

  const matrixQ = captchaMode === "matrix" ? parseMatrixQuestion(captchaQuestion) : null;
  const matrixLatexHtml = matrixQ ? renderMatrixLatex(matrixQ.A, matrixQ.B) : "";

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
      // Force-reset-on-next-login branch: the server has atomically
      // rotated the password because an admin/owner previously
      // triggered "强制重置密码" on this account. We MUST surface the
      // new plaintext to the user before redirecting — otherwise the
      // user lands in the dashboard, the session is fresh, and they
      // never see the only place their new credential will ever be
      // displayed. Stash the new password + next URL; the modal
      // dismissal triggers the redirect.
      if (resp.new_password) {
        setForcedNewPassword({ password: resp.new_password, next: resp.next || next });
        return;
      }
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
      // Passkey login never triggers must_reset_password (the server
      // only rotates on a successful PASSWORD login), but defend in
      // depth anyway in case a future server change does.
      const np = (resp as any).new_password as string | undefined;
      if (np) {
        setForcedNewPassword({ password: np, next: resp.next || next });
        return;
      }
      router.push(resp.next || next);
    } catch (err: any) {
      setError(err.message || "Passkey 登录失败。");
    } finally {
      setPasskeyLoading(false);
    }
  };

  const copyForcedNewPassword = async () => {
    if (!forcedNewPassword) return;
    try {
      await navigator.clipboard.writeText(forcedNewPassword.password);
      setError(""); // clear any previous error display
      // Reuse the form-error region to flash a "copied" hint. Not
      // great UX, but importing the admin toast system on a public
      // page is overkill for a one-shot success state.
      setError("已复制到剪贴板。");
      window.setTimeout(() => setError(""), 2000);
    } catch {
      setError("复制失败，请手动选中密码复制。");
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
            <div className="matrix-eq" data-testid="matrix-question"
              // Expose the original JSON question in dev/test for Playwright
              // and ad-hoc DOM inspection. Production builds drop it
              // (React omits undefined attributes) — the same string is
              // already on the wire in the /auth/csrf response, so we're
              // not gaining security by hiding it; we're just not shipping
              // a debug aid to real users.
              data-question={process.env.NODE_ENV !== "production" ? captchaQuestion : undefined}
              // KaTeX-rendered HTML for the LaTeX matrix equation. Using
              // dangerouslySetInnerHTML is safe here because the input is
              // two integer arrays we generate ourselves and feed through
              // katex.renderToString — no user-controlled text reaches the
              // LaTeX parser.
              dangerouslySetInnerHTML={{ __html: matrixLatexHtml }}
            />
          ) : (
            <span className="captcha-question">{captchaQuestion}</span>
          )}

          {captchaMode === "matrix" ? (
            <>
              <MatrixInput
                cells={captchaCells}
                invalid={(v) => !isCellValid(v)}
                refs={cellRefs}
                onChange={handleCellChange}
                onKeyDown={handleCellKeyDown}
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

      {/* Force-reset-on-next-login modal. The user just authenticated
          with their old password, but the server has rotated it to
          the value shown here — and this modal is the ONLY place that
          value will ever be displayed. Persistent (no Escape / backdrop
          close) so the user can't accidentally bypass the copy step.
          The "我已复制" button both copies and redirects in one go, so
          the user is never more than one click away from the dashboard
          after acknowledging the new credential. */}
      <AdminModal
        open={!!forcedNewPassword}
        onClose={() => undefined}
        title="🔐 管理员已为你重置密码"
        size="sm"
        persistent
        footer={
          forcedNewPassword ? (
            <>
              <button
                type="button"
                className="admin-btn admin-btn-outline"
                onClick={copyForcedNewPassword}
              >
                📋 复制新密码
              </button>
              <button
                type="button"
                className="admin-btn admin-btn-primary"
                onClick={() => {
                  const next = forcedNewPassword.next;
                  setForcedNewPassword(null);
                  router.push(next);
                }}
              >
                我已复制，继续
              </button>
            </>
          ) : null
        }
      >
        {forcedNewPassword && (
          <div>
            <p style={{ margin: "0 0 12px", lineHeight: 1.6 }}>
              你的账号密码已被管理员重置。新密码只显示这一次，请现在复制并妥善保管。
            </p>
            <div className="admin-generated-password">
              <code style={{ wordBreak: "break-all", fontSize: "1rem" }}>{forcedNewPassword.password}</code>
            </div>
            <div className="admin-form-hint" style={{ marginTop: 12 }}>
              关闭此弹窗后密码将无法再次查看。如遗失，请联系管理员再次重置。
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}