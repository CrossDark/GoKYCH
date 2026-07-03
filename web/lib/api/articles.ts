import { request, cache, isSSR, apiUrl } from "./client";
import type { Article, ArticleListResult, ArticleDetail } from "@/lib/types";

export function listArticles(
  type?: string,
  page = 1,
  authorId?: number
) {
  const q = new URLSearchParams();
  if (type) q.set("type", type);
  if (authorId) q.set("author_id", String(authorId));
  q.set("page", String(page));
  const opts: any = { anon: true, next: { tags: ["articles"] } };
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
  return request<ArticleDetail>(`/articles/${type}/${slug}`);
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