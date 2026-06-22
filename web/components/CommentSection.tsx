"use client";

import { useState } from "react";
import { addComment } from "@/lib/api";
import type { Comment } from "@/lib/types";

interface Props {
  articleType: string;
  articleSlug: string;
  csrfToken: string;
  initialComments: Comment[];
}

export function CommentSection({
  articleType,
  articleSlug,
  csrfToken,
  initialComments,
}: Props) {
  const [comments, setComments] = useState<Comment[]>(initialComments);
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!content.trim()) {
      setError("评论内容不能为空。");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      const cm = await addComment(articleType, articleSlug, csrfToken, {
        content: content.trim(),
        author_name: name.trim() || undefined,
      });
      setComments((prev) => [...prev, cm]);
      setContent("");
    } catch (err: any) {
      setError(err.message || "评论失败。");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="comment-section">
      <h3>评论 ({comments.length})</h3>

      {comments.length > 0 && (
        <div className="comment-list">
          {comments.map((cm) => (
            <div key={cm.id} className="comment-item">
              <div className="comment-header">
                <span className="comment-author">{cm.author_name}</span>
                <time className="comment-time">
                  {new Date(cm.created_at).toLocaleString("zh-CN")}
                </time>
              </div>
              <div className="comment-content">{cm.content}</div>
            </div>
          ))}
        </div>
      )}

      <form className="comment-form" onSubmit={handleSubmit}>
        <input
          type="text"
          className="comment-name-input"
          placeholder="昵称（选填，匿名）"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={64}
        />
        <textarea
          className="comment-content-input"
          placeholder="写下你的评论…"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          rows={3}
          required
        />
        {error && <p className="comment-error">{error}</p>}
        <button
          type="submit"
          className="btn btn-primary"
          disabled={submitting}
        >
          {submitting ? "提交中…" : "发表评论"}
        </button>
      </form>
    </section>
  );
}
