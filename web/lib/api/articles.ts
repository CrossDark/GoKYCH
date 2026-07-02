import { request, cache, isSSR } from "./client";
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
    next: { tags: ["articles", `article:${type}:${slug}`], revalidate: 300 },
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