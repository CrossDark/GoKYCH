"use client";

import { useEffect, useState } from "react";
import { getCsrf, listAdminTags, createTag, renameTag, deleteAdminTag } from "@/lib/api";
import type { AdminTag } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";

export default function AdminTags() {
  const [csrf, setCsrf] = useState("");
  const [tags, setTags] = useState<AdminTag[]>([]);
  const [loading, setLoading] = useState(true);
  const toast = useToast();
  const [msg, setMsg] = useState<{ kind: "success" | "error" | "info"; text: string } | null>(null);
  const [newName, setNewName] = useState("");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [search, setSearch] = useState("");
  const [pendingDelete, setPendingDelete] = useState<AdminTag | null>(null);

  const load = () => {
    if (!csrf) return;
    setLoading(true);
    listAdminTags(csrf)
      .then(setTags)
      .catch(() => setTags([]))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
    });
  }, []);

  useEffect(() => {
    if (csrf) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [csrf]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    setMsg(null);
    setSubmitting(true);
    try {
      const r = await createTag(csrf, name);
      const text = r.existed ? `标签「${name}」已存在。` : `标签「${name}」已创建。`;
      setMsg({ kind: r.existed ? "info" : "success", text });
      if (r.existed) toast.info(text);
      else toast.success(text);
      setNewName("");
      load();
    } catch (err: any) {
      const text = err.message || "创建失败。";
      setMsg({ kind: "error", text });
      toast.error(text);
    } finally {
      setSubmitting(false);
    }
  };

  const startRename = (t: AdminTag) => {
    setEditingId(t.id);
    setEditingName(t.name);
  };

  const cancelRename = () => {
    setEditingId(null);
    setEditingName("");
  };

  const commitRename = async (id: number) => {
    const name = editingName.trim();
    if (!name) {
      cancelRename();
      return;
    }
    setMsg(null);
    try {
      await renameTag(csrf, id, name);
      toast.success("标签已重命名。");
      cancelRename();
      load();
    } catch (err: any) {
      const text = err.message || "重命名失败。";
      setMsg({ kind: "error", text });
      toast.error(text);
    }
  };

  const handleDelete = (t: AdminTag) => setPendingDelete(t);
  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const t = pendingDelete;
    try {
      await deleteAdminTag(csrf, t.id);
      toast.success(`标签「${t.name}」已删除。`);
      load();
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err;
    } finally {
      setPendingDelete(null);
    }
  };

  const filtered = tags.filter((t) =>
    !search || t.name.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="admin-tags">
      <div className="admin-page-header">
        <div>
          <h1>标签管理</h1>
          <div className="admin-page-subtitle">
            {tags.length > 0 ? `共 ${tags.length} 个标签${search ? ` · 匹配 ${filtered.length}` : ""}` : "加载中…"}
          </div>
        </div>
      </div>

      {msg && (
        <div className={`admin-notice admin-notice-${msg.kind}`}>
          <span className="admin-notice-icon">{msg.kind === "success" ? "✓" : msg.kind === "error" ? "✕" : "ℹ"}</span>
          <div className="admin-notice-content">{msg.text}</div>
        </div>
      )}

      {/* New tag */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>＋ 新建标签</h2>
        </div>
        <div className="admin-card-body">
          <form onSubmit={handleCreate} className="admin-form">
            <div className="admin-form-row" style={{ gridTemplateColumns: "2fr 1fr" }}>
              <div className="admin-form-group">
                <label htmlFor="tag-name">
                  标签名
                  <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
                </label>
                <input
                  id="tag-name"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="例如：typescript / react / backend"
                  maxLength={64}
                  required
                  aria-describedby="tag-name-hint"
                />
                <div id="tag-name-hint" className="admin-form-hint">
                  最多 64 字符，建议用小写 + 短横线
                </div>
              </div>
              <div className="admin-form-group" style={{ justifyContent: "flex-end" }}>
                <label>&nbsp;</label>
                <button
                  type="submit"
                  className={`admin-btn admin-btn-primary ${submitting ? "admin-btn-loading" : ""}`}
                  disabled={submitting}
                >
                  创建标签
                </button>
              </div>
            </div>
          </form>
        </div>
      </div>

      {/* Search */}
      <div className="admin-filter">
        <span className="admin-filter-label">搜索：</span>
        <div className="admin-search-box" style={{ maxWidth: 300 }}>
          <span className="admin-search-box-icon">🔍</span>
          <input
            placeholder="按标签名搜索"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* List */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>🏷 标签列表</h2>
        </div>
        <div className="admin-card-body no-padding">
          {loading ? (
            <div className="admin-empty">加载中…</div>
          ) : tags.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">🏷️</span>
              <div className="admin-empty-title">还没有标签</div>
              <div>通过上方表单新建，或在编辑文章时新增</div>
            </div>
          ) : filtered.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">🔍</span>
              <div className="admin-empty-title">没有匹配的标签</div>
            </div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th className="col-date">关联文章</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((t) => (
                  <tr key={t.id}>
                    <td className="col-title">
                      {editingId === t.id ? (
                        <input
                          autoFocus
                          value={editingName}
                          onChange={(e) => setEditingName(e.target.value)}
                          onBlur={() => commitRename(t.id)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") commitRename(t.id);
                            else if (e.key === "Escape") cancelRename();
                          }}
                          maxLength={64}
                          style={{ fontSize: "0.88rem", padding: "0.3rem 0.5rem" }}
                        />
                      ) : (
                        <span
                          className="admin-tag-name"
                          onDoubleClick={() => startRename(t)}
                          title="双击重命名"
                        >
                          🏷 {t.name}
                        </span>
                      )}
                    </td>
                    <td>
                      <span className={`admin-badge ${t.count > 0 ? "admin-badge-primary" : "admin-badge-neutral"}`}>
                        {t.count} 篇
                      </span>
                    </td>
                    <td className="col-actions">
                      {editingId === t.id ? (
                        <>
                          <button className="admin-btn admin-btn-primary admin-btn-sm" onClick={() => commitRename(t.id)}>保存</button>
                          <button className="admin-btn admin-btn-ghost admin-btn-sm" onClick={cancelRename}>取消</button>
                        </>
                      ) : (
                        <>
                          <button className="admin-btn admin-btn-secondary admin-btn-sm" onClick={() => startRename(t)}>✏️ 重命名</button>
                          <button className="admin-btn admin-btn-danger admin-btn-sm" onClick={() => handleDelete(t)}>🗑 删除</button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <AdminConfirm
        open={!!pendingDelete}
        title="删除标签"
        message={pendingDelete ? `确定要删除标签「${pendingDelete.name}」吗？这会从所有引用此标签的文章上解除关联。` : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}
