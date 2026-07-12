"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useApp } from "@/components/AppProviders";
import { listDiscussions, createDiscussion, type Discussion } from "@/lib/api";
import { Pagination } from "@/components/Pagination";
import { getCsrf } from "@/lib/api";
import { UserAvatar } from "@/components/admin/UserAvatar";

export default function DiscussionListPage() {
  const { user } = useApp();
  const [discussions, setDiscussions] = useState<Discussion[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [csrf, setCsrf] = useState("");
  const [creating, setCreating] = useState(false);
  const [formData, setFormData] = useState({ title: "", content: "", format: "md" });

  useEffect(() => {
    loadDiscussions(page);
  }, [page]);

  useEffect(() => {
    if (user) {
      getCsrf().then((response) => setCsrf(response.csrf_token));
    }
  }, [user]);

  const loadDiscussions = async (p: number) => {
    setLoading(true);
    try {
      const res = await listDiscussions(p);
      setDiscussions(res.discussions);
      setTotal(res.total);
    } catch {
      setDiscussions([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async () => {
    if (!user || !csrf) return;
    setCreating(true);
    try {
      await createDiscussion(csrf, formData.title, formData.content, formData.format);
      setShowCreateModal(false);
      setFormData({ title: "", content: "", format: "md" });
      loadDiscussions(1);
    } catch (err) {
      console.error("创建讨论失败:", err);
    } finally {
      setCreating(false);
    }
  };

  const formatNames: Record<string, string> = {
    md: "Markdown",
    bbcode: "BBCode",
    html: "HTML",
  };

  return (
    <div className="discussion-list-page">
      <div className="page-header">
        <h1>💬 讨论</h1>
        {user && (
          <button
            className="admin-btn admin-btn-primary"
            onClick={() => setShowCreateModal(true)}
          >
            + 发起新讨论
          </button>
        )}
      </div>

      {loading ? (
        <div className="loading">加载中...</div>
      ) : discussions.length === 0 ? (
        <div className="empty-state">
          <p>还没有讨论</p>
        </div>
      ) : (
        <div className="discussion-list">
          {discussions.map((d) => (
            <Link
              key={d.id}
              href={`/discuss/${d.slug}`}
              className="discussion-item"
            >
              <div className="discussion-header">
                <h2>{d.title}</h2>
                <span className="discussion-format">{formatNames[d.format] || d.format}</span>
              </div>
              <div className="discussion-preview">
                {d.content_html ? (
                  <div dangerouslySetInnerHTML={{ __html: d.content_html }} className="discussion-preview-html" />
                ) : (
                  <p>{d.content.slice(0, 100)}...</p>
                )}
              </div>
              <div className="discussion-meta">
                {d.author_id ? (
                  <span className="discussion-author">
                    <UserAvatar user={{ username: d.author_name || "", nickname: d.author_nickname || "", avatar: d.author_avatar || "" }} size={20} />
                    <span>{d.author_nickname || d.author_name}</span>
                  </span>
                ) : (
                  <span className="discussion-author">{d.author_name || "匿名"}</span>
                )}
                <time>{new Date(d.created_at).toLocaleString("zh-CN")}</time>
                <span className="discussion-replies">💬 {d.reply_count} 回复</span>
              </div>
            </Link>
          ))}
        </div>
      )}

      {total > 20 && !loading && (
        <Pagination
          page={page}
          totalPages={Math.ceil(total / 20)}
          basePath="/discuss"
        />
      )}

      {showCreateModal && (
        <div className="modal-overlay" onClick={() => setShowCreateModal(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3>发起新讨论</h3>
              <button className="modal-close" onClick={() => setShowCreateModal(false)}>✕</button>
            </div>
            <div className="modal-body">
              <div className="form-group">
                <label>标题</label>
                <input
                  type="text"
                  value={formData.title}
                  onChange={(e) => setFormData({ ...formData, title: e.target.value })}
                  placeholder="输入讨论标题"
                  className="form-input"
                />
              </div>
              <div className="form-group">
                <label>格式</label>
                <select
                  value={formData.format}
                  onChange={(e) => setFormData({ ...formData, format: e.target.value })}
                  className="form-input"
                >
                  <option value="md">Markdown</option>
                  <option value="bbcode">BBCode</option>
                  <option value="html">HTML</option>
                </select>
              </div>
              <div className="form-group">
                <label>内容</label>
                <textarea
                  value={formData.content}
                  onChange={(e) => setFormData({ ...formData, content: e.target.value })}
                  placeholder="输入讨论内容..."
                  className="form-textarea"
                  rows={10}
                />
              </div>
            </div>
            <div className="modal-footer">
              <button className="btn btn-secondary" onClick={() => setShowCreateModal(false)}>取消</button>
              <button className="btn btn-primary" onClick={handleCreate} disabled={!formData.title.trim() || creating}>
                {creating ? "创建中..." : "发起讨论"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}