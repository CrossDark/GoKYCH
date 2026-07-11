import { request } from "./client";

export interface Discussion {
  id: number;
  slug: string;
  title: string;
  content: string;
  content_html?: string;
  format: string;
  author_id?: number;
  author_name?: string;
  author_nickname?: string;
  author_avatar?: string;
  reply_count: number;
  created_at: string;
  updated_at: string;
}

export interface DiscussionReply {
  id: number;
  discussion_id: number;
  content: string;
  content_html?: string;
  format: string;
  user_id?: number;
  author_name: string;
  author_nickname?: string;
  author_avatar?: string;
  created_at: string;
}

export interface DiscussionListResponse {
  discussions: Discussion[];
  total: number;
  page: number;
}

export interface DiscussionDetailResponse {
  discussion: Discussion;
  replies: DiscussionReply[];
}

export function listDiscussions(page: number = 1) {
  return request<DiscussionListResponse>("/discussions", {
    method: "GET",
    params: { page },
  });
}

export function getDiscussion(slug: string) {
  return request<DiscussionDetailResponse>(`/discussions/${slug}`);
}

export function createDiscussion(csrf: string, title: string, content: string, format: string) {
  return request<Discussion>("/discussions", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ title, content, format }),
  });
}

export function addDiscussionReply(csrf: string, slug: string, content: string, format: string) {
  return request<DiscussionReply>(`/discussions/${slug}/replies`, {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ content, format }),
  });
}

export function deleteDiscussion(csrf: string, slug: string) {
  return request(`/discussions/${slug}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}
