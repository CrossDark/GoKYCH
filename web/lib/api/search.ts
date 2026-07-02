import { request, isSSR } from "./client";
import type { ArticleListResult } from "@/lib/types";

export function search(q: string, page = 1) {
  return request<ArticleListResult>(
    `/search?q=${encodeURIComponent(q)}&page=${page}`,
    isSSR ? { next: { revalidate: 30 }, anon: true } : undefined
  );
}