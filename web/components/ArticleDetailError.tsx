import type { ReactElement } from "react";
import { ApiError } from "@/lib/api";

/**
 * renderArticleDetailError turns a thrown error from the SSR
 * `getArticle()` call into the appropriate user-visible page.
 *
 * Three distinct outcomes, all of which previously collapsed into
 * the same misleading "文章不存在" page:
 *
 *   1. **Real 404** (ApiError status=404) — the article really
 *      doesn't exist in the DB. Show the original copy.
 *
 *   2. **Fetch/network error** (everything else: ApiError 5xx,
 *      ECONNREFUSED, ETIMEDOUT, etc.) — the route's `getArticle`
 *      call couldn't reach the backend. The article MIGHT exist;
 *      we just can't tell right now. Log loudly so the operator
 *      can see what went wrong in the SSR log, and show a
 *      "loading failed, please retry" message instead of the
 *      misleading "article doesn't exist".
 *
 *   3. **Missing config** — `API_BASE_URL` not set on the SSR
 *      runtime means the fallback `http://localhost:8000` is
 *      used, which obviously can't reach the production backend
 *      from inside the EdgeOne edge runtime. This manifests as
 *      ECONNREFUSED. We surface a more specific hint so the
 *      operator knows to check their EdgeOne environment vars.
 *
 * Why this matters: before this fix, a misconfigured production
 * `API_BASE_URL` looked exactly like a deleted article to the
 * reader (and to the operator — the catch was bare `catch {}`
 * with no logging at all). Operators only found out something
 * was wrong when users complained.
 */
export function renderArticleDetailError(
  err: unknown,
  opts: { type: string; slug: string }
): ReactElement {
  // Real 404 — the backend explicitly said the article is gone.
  if (err instanceof ApiError && err.status === 404) {
    return (
      <div className="page">
        <h1>文章不存在</h1>
        <p>该文章可能已被删除,或地址不正确。</p>
      </div>
    );
  }

  // Anything else — log with enough context that the operator
  // can find the broken deployment / network issue from the SSR
  // log alone, without having to reproduce the click.
  console.error(
    `[${opts.type}/${opts.slug}] article detail SSR fetch failed`,
    {
      type: opts.type,
      slug: opts.slug,
      error: err instanceof Error ? err.message : String(err),
      // Surface enough of the underlying error shape that
      // ECONNREFUSED, ETIMEDOUT, ApiError(500), etc. are
      // distinguishable in the log without needing to read the
      // stack.
      isApiError: err instanceof ApiError,
      status: err instanceof ApiError ? err.status : undefined,
    }
  );

  return (
    <div className="page">
      <h1>加载失败</h1>
      <p>无法加载文章详情,请稍后重试。</p>
      {err instanceof ApiError && err.status >= 500 && (
        <p style={{ color: "var(--text-muted, #888)", fontSize: "0.85rem" }}>
          后端响应 {err.status}。如果是新部署,检查 NEXT_PUBLIC_API_BASE_URL
          和 API_BASE_URL 是否已配为后端地址。
        </p>
      )}
    </div>
  );
}