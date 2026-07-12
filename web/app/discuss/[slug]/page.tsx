"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useApp } from "@/components/AppProviders";
import { getDiscussion, addDiscussionReply, deleteDiscussion, type Discussion, type DiscussionReply } from "@/lib/api";
import { getCsrf } from "@/lib/api";
import { UserAvatar } from "@/components/admin/UserAvatar";

export default function DiscussionDetailPage() {
  const params = useParams<{ slug: string }>();
  const router = useRouter();
  const { user } = useApp();
  const [discussion, setDiscussion] = useState<Discussion | null>(null);
  const [replies, setReplies] = useState<DiscussionReply[]>([]);
  const [loading, setLoading] = useState(true);
  const [csrf, setCsrf] = useState("");
  const [replying, setReplying] = useState(false);
  const [replyContent, setReplyContent] = useState("");
  const [replyFormat, setReplyFormat] = useState("md");
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    loadDiscussion();
  }, [params.slug]);

  useEffect(() => {
    if (user) {
      getCsrf().then((response) => setCsrf(response.csrf_token));
    }
  }, [user]);

  const loadDiscussion = async () => {
    setLoading(true);
    try {
      const res = await getDiscussion(params.slug);
      setDiscussion(res.discussion);
      setReplies(res.replies);
    } catch (err) {
      console.error("加载讨论失败:", err);
    } finally {
      setLoading(false);
    }
  };

  const handleReply = async () => {
    if (!user || !csrf || !replyContent.trim()) return;
    setReplying(true);
    try {
      await addDiscussionReply(csrf, params.slug, replyContent, replyFormat);
      setReplyContent("");
      loadDiscussion();
    } catch (err) {
      console.error("回复失败:", err);
    } finally {
      setReplying(false);
    }
  };

  const handleDelete = async () => {
    if (!user || !csrf) return;
    if (!confirm("确定要删除这个讨论吗？")) return;
    setDeleting(true);
    try {
      await deleteDiscussion(csrf, params.slug);
      router.push("/discuss");
    } catch (err) {
      console.error("删除失败:", err);
    } finally {
      setDeleting(false);
    }
  };

  const canDelete = () => {
    if (!user) return false;
    if (user.role === "owner" || user.role === "admin") return true;
    if (discussion?.author_id === user.id) return true;
    return false;
  };

  const formatNames: Record<string, string> = {
    md: "Markdown",
    bbcode: "BBCode",
    html: "HTML",
  };

  if (loading) {
    return <div className="loading">加载中...</div>;
  }

  if (!discussion) {
    return (
      <div className="error-page">
        <p>讨论不存在或已被删除。</p>
        <Link href="/discuss" className="btn btn-primary">返回讨论列表</Link>
      </div>
    );
  }

  return (
    <div className="discussion-detail-page">
      <div className="discussion-header">
        <Link href="/discuss" className="btn btn-secondary">← 返回讨论列表</Link>
        <h1>{discussion.title}</h1>
        {canDelete() && (
          <button className="btn btn-danger" onClick={handleDelete} disabled={deleting}>
            {deleting ? "删除中..." : "删除讨论"}
          </button>
        )}
      </div>

      <div className="discussion-meta">
        {discussion.author_id ? (
          <span className="discussion-author">
            <UserAvatar user={{ username: discussion.author_name || "", nickname: discussion.author_nickname || "", avatar: discussion.author_avatar || "" }} size={24} />
            <span>{discussion.author_nickname || discussion.author_name}</span>
          </span>
        ) : (
          <span className="discussion-author">{discussion.author_name || "匿名"}</span>
        )}
        <span className="discussion-format">{formatNames[discussion.format] || discussion.format}</span>
        <time>{new Date(discussion.created_at).toLocaleString("zh-CN")}</time>
      </div>

      <div className="discussion-content">
        {discussion.content_html ? (
          <div dangerouslySetInnerHTML={{ __html: discussion.content_html }} />
        ) : (
          <pre>{discussion.content}</pre>
        )}
      </div>

      <div className="replies-section">
        <h2>💬 回复 ({replies.length})</h2>
        {replies.length === 0 ? (
          <p className="empty-replies">暂无回复，来说点什么吧！</p>
        ) : (
          <div className="replies-list">
            {replies.map((r) => (
              <div key={r.id} className="reply-item">
                <div className="reply-author">
                  {r.user_id ? (
                    <>
                      <UserAvatar user={{ username: r.author_name, nickname: r.author_nickname || "", avatar: r.author_avatar || "" }} size={24} />
                      <span>{r.author_nickname || r.author_name}</span>
                    </>
                  ) : (
                    <span>{r.author_name}</span>
                  )}
                  <time>{new Date(r.created_at).toLocaleString("zh-CN")}</time>
                  <span className="reply-format">{formatNames[r.format] || r.format}</span>
                </div>
                <div className="reply-content">
                  {r.content_html ? (
                    <div dangerouslySetInnerHTML={{ __html: r.content_html }} />
                  ) : (
                    <p>{r.content}</p>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}

        {user && (
          <div className="reply-form">
            <h3>发表回复</h3>
            <div className="form-group">
              <label>格式</label>
              <select
                value={replyFormat}
                onChange={(e) => setReplyFormat(e.target.value)}
                className="form-input"
              >
                <option value="md">Markdown</option>
                <option value="bbcode">BBCode</option>
                <option value="html">HTML</option>
              </select>
            </div>
            <div className="form-group">
              <textarea
                value={replyContent}
                onChange={(e) => setReplyContent(e.target.value)}
                placeholder="输入回复内容..."
                className="form-textarea"
                rows={5}
              />
            </div>
            <button className="btn btn-primary" onClick={handleReply} disabled={!replyContent.trim() || replying}>
              {replying ? "回复中..." : "发送回复"}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
