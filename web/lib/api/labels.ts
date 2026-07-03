import { request, cache, dedupClient, isSSR } from "./client";
import type { TagWithCount, ArticleListResult } from "@/lib/types";

const _listLabelsSSR = cache(() => request<TagWithCount[]>("/labels", { anon: true, next: { tags: ["labels"], revalidate: 600 } }));

export function listLabels() {
  if (isSSR) return _listLabelsSSR();
  return dedupClient("labels", () => request<TagWithCount[]>("/labels"));
}

export function getLabelArticles(tag: string, page = 1) {
  return request<ArticleListResult>(
    `/labels/${encodeURIComponent(tag)}?page=${page}`,
    { anon: true }
  );
}