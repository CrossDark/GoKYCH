import { request, cache, isSSR, apiUrl } from "./client";
import type {
  Article,
  ArticleListResult,
  ArticleDetail,
  RevisionListResult,
  RevisionDetail,
  RevisionDiffResult,
} from "@/lib/types";

export function listArticles(
  type?: string,
  page = 1,
  authorId?: number
) {
  const q = new URLSearchParams();
  if (type) q.set("type", type);
  if (authorId) q.set("author_id", String(authorId));
  q.set("page", String(page));
  const opts: { anon: boolean; next: { tags?: string[]; revalidate?: number } } = { anon: true, next: { tags: ["articles"] } };
  if (authorId) {
    opts.anon = false;
    opts.next = { revalidate: 0 };
  }
  return request<ArticleListResult>(
    `/articles?${q.toString()}`, opts
  );
}

const _getArticleSSR = cache((type: string, slug: string) =>
  request<ArticleDetail>(`/articles/${type}/${slug}`, {
    anon: true,
    next: { tags: ["articles", `article:${type}:${slug}`], revalidate: 86400 },
  })
);

export function getArticle(type: string, slug: string) {
  if (isSSR) return _getArticleSSR(type, slug);
  // The server sends `Cache-Control: private, max-age=30` for the
  // authenticated read (see api/articles.go getArticle). That cache is
  // fine for the public anonymous reader — it lives behind the edge —
  // but for the admin editor it would mean a page reload within 30s of
  // a save/restore still shows the pre-mutation content. Bypass the
  // browser cache here so the editor always reflects the latest server
  // state on mount. The admin editor navigates often anyway, so the
  // 30s reuse wasn't buying much in this path.
  return request<ArticleDetail>(`/articles/${type}/${slug}`, {
    cache: "no-store",
  });
}

export function createArticle(
  type: string,
  csrf: string,
  body: { slug: string; title: string; content: string; tags?: string[] }
) {
  return request<Article>(
    `/articles?type=${type}`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function updateArticle(
  type: string,
  slug: string,
  csrf: string,
  body: { title: string; content: string; tags?: string[] }
) {
  return request<Article>(
    `/articles/${type}/${slug}`,
    {
      method: "PUT",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function deleteArticle(type: string, slug: string, csrf: string) {
  return request<{ status: string }>(
    `/articles/${type}/${slug}`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

// getAllArticleSlugs walks the article list endpoint's pagination and
// returns every slug for the given type. It is called from
// generateStaticParams at BUILD time so Next.js prerenders every existing
// article as ○ Static — EdgeOne then serves them as static assets (no
// SSR/SCF on first hit, ~tens of ms instead of ~600ms).
//
// Articles created after a build are still covered by on-demand ISR
// (dynamicParams=true on the [slug] route) plus the revalidate webhook, so
// nothing goes stale. Failures degrade gracefully: any network/parse error
// returns [] so the build never breaks — routes just fall back to on-demand
// ISR. `cache: "no-store"` ensures the build sees the current article list,
// not a stale cached copy.
export async function getAllArticleSlugs(type: string): Promise<{ slug: string }[]> {
  const out: { slug: string }[] = [];
  try {
    for (let page = 1; page < 1000; page++) {
      const res = await fetch(
        apiUrl(`/api/articles?type=${encodeURIComponent(type)}&page=${page}`),
        { cache: "no-store" }
      );
      if (!res.ok) break;
      const data = (await res.json()) as ArticleListResult;
      for (const a of data.articles) out.push({ slug: a.slug });
      if (page >= data.total_pages) break;
    }
  } catch {
    return [];
  }
  return out;
}

// ── Revision history (V5) ────────────────────────────────────────────
//
// Thin wrappers over the public read endpoints shipped in V3 (bea0a9e
// / 987e17b) and the write endpoint shipped in V4 (62b5965). All are
// anon-readable except restoreRevision which is a logged-in mutation.

export function listRevisions(
  type: string,
  slug: string,
  page = 1,
  perPage = 20
) {
  const q = new URLSearchParams();
  q.set("page", String(page));
  q.set("per_page", String(perPage));
  // History is short, queried rarely, and the user expects to see the
  // freshest rows after a save / restore — bypass the browser cache so
  // a same-tab reload after a restore doesn't show the pre-restore list
  // (backend serves Cache-Control: max-age=30 + swr=60). Matches the
  // pattern used by getAllArticleSlugs above.
  return request<RevisionListResult>(
    `/articles/${type}/${slug}/revisions?${q.toString()}`,
    { anon: true, cache: "no-store" }
  );
}

export function getRevision(type: string, slug: string, seq: number) {
  return request<RevisionDetail>(
    `/articles/${type}/${slug}/revisions/${seq}`,
    { anon: true }
  );
}

export function getRevisionDiff(
  type: string,
  slug: string,
  from: number,
  to: number
) {
  const q = new URLSearchParams();
  q.set("from", String(from));
  q.set("to", String(to));
  return request<RevisionDiffResult>(
    `/articles/${type}/${slug}/revisions/diff?${q.toString()}`,
    { anon: true }
  );
}

export function restoreRevision(
  type: string,
  slug: string,
  seq: number,
  csrf: string,
  message: string
) {
  return request<{ article: Article; restored_to: number; compile_status?: unknown }>(
    `/articles/${type}/${slug}/revisions/${seq}/restore`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify({ message }),
    }
  );
}