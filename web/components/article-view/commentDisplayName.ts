import type { Comment } from "@/lib/types";

export function commentDisplayName(c: Comment): string {
  return c.author_nickname && c.author_nickname.trim() !== ""
    ? c.author_nickname
    : (c.author_name || "匿名");
}
