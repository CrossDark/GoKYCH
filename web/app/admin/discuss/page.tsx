"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { listDiscussions, deleteDiscussion, type Discussion } from "@/lib/api";
import { getCsrf, getMe } from "@/lib/api";
import { useToast } from "@/lib/admin-feedback";
import { UserAvatar } from "@/components/admin/UserAvatar";
import { Pagination } from "@/components/Pagination";
import type { User } from "@/lib/types";

export default function AdminDiscussionPage() {
  const router = useRouter();
  const toast = useToast();
  const [discussions, setDiscussions] = useState<Discussion[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [csrf, setCsrf] = useState("");
  const [user, setUser] = useState<User | null>(null);

  useEffect(() => {
    loadDiscussions(page);
  }, [page, user]);

  useEffect(() => {
    getCsrf().then((response) => setCsrf(response.csrf_token));
    getMe().then((response) => setUser(response.user));
  }, []);

  const loadDiscussions = async (p: number) => {
    setLoading(true);
    try {
      const authorId = user?.role === "user" ? user.id : undefined;
      const res = await listDiscussions(p, authorId);
      setDiscussions(res.discussions);
      setTotal(res.total);
    } catch {
      setDiscussions([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (slug: string, id: number) => {
    if (!csrf) return;
    if (!confirm("确定要删除这个讨论吗？")) return;
    setDeletingId(id);
    try {
      await deleteDiscussion(csrf, slug);
      toast.success("讨论已删除");
      loadDiscussions(page);
    } catch (err) {
      toast.error("删除失败");
    } finally {
      setDeletingId(null);
    }
  };

  const formatNames: Record<string, string> = {
    md: "Markdown",
    bbcode: "BBCode",
    html: "HTML",
  };

  return (
    <div className="admin-section">
      <div className="admin-section-header">
        <h1>{user?.role === "user" ? "我的讨论" : "讨论管理"}</h1>
      </div>

      {loading ? (
        <div className="loading">加载中...</div>
      ) : discussions.length === 0 ? (
        <div className="empty-state">
          <p>{user?.role === "user" ? "暂无我的讨论" : "暂无讨论内容"}</p>
        </div>
      ) : (
        <div className="admin-table-wrapper">
          <table className="admin-table">
            <thead>
              <tr>
                <th>标题</th>
                <th>作者</th>
                <th>格式</th>
                <th>回复数</th>
                <th>创建时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {discussions.map((d) => (
                <tr key={d.id}>
                  <td>
                    <a href={`/discuss/${d.slug}`} target="_blank" rel="noopener noreferrer">
                      {d.title}
                    </a>
                  </td>
                  <td>
                    {d.author_id ? (
                      <span className="discussion-author">
                        <UserAvatar user={{ username: d.author_name || "", nickname: d.author_nickname || "", avatar: d.author_avatar || "" }} size={20} />
                        <span>{d.author_nickname || d.author_name}</span>
                      </span>
                    ) : (
                      <span>{d.author_name || "匿名"}</span>
                    )}
                  </td>
                  <td>{formatNames[d.format] || d.format}</td>
                  <td>{d.reply_count}</td>
                  <td>{new Date(d.created_at).toLocaleString("zh-CN")}</td>
                  <td>
                    <button
                      className="admin-btn admin-btn-danger admin-btn-sm"
                      onClick={() => handleDelete(d.slug, d.id)}
                      disabled={deletingId === d.id}
                    >
                      {deletingId === d.id ? "删除中..." : "删除"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {total > 20 && !loading && (
        <Pagination
          page={page}
          totalPages={Math.ceil(total / 20)}
          basePath="/admin/discuss"
        />
      )}
    </div>
  );
}