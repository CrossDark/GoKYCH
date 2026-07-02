import { request } from "./client";
import type { Comment } from "@/lib/types";

export function addComment(
  type: string, slug: string, csrf: string,
  body: { content: string; author_name?: string }
) {
  return request<Comment>(
    `/articles/${type}/${slug}/comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function getLineComments(type: string, slug: string) {
  return request<{ counts: Record<number, number>; comments: (Comment & { line_number: number })[] }>(
    `/articles/${type}/${slug}/line-comments`
  );
}

export function addLineComment(
  type: string, slug: string, csrf: string,
  body: { line_number: number; content: string; author_name?: string }
) {
  return request<Comment>(
    `/articles/${type}/${slug}/line-comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}