"use client";

import { useEffect, useState } from "react";
import { getCsrf, listAdminTags, createTag, renameTag, deleteAdminTag } from "@/lib/api";
import type { AdminTag } from "@/lib/types";

export default function AdminTags() {
  const [csrf, setCsrf] = useState("");
  const [tags, setTags] = useState<AdminTag[]>([]);
  const [loading, setLoading] = useState(true);
  const [msg, setMsg] = useState<{ kind: "success" | "error" | "info"; text: string } | null>(null);
  const [newName, setNewName] = useState("");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [search, setSearch] = useState("");

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
      setMsg({
        kind: r.existed ? "info" : "success",
        text: r.existed ? `标签「${name}」已存在。` : `标签「${name}」已创建。`,
      });
      setNewName("");
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "创建失败。" });
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
      setMsg({ kind: "success", text: "标签已重命名。" });
      cancelRename();
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "重命名失败。" });
    }
  };

  const handleDelete = async (t: AdminTag) => {
    if (!confirm(`确定删除标签「${t.name}」吗？这会从所有引用此标签的文章上解除关联。`)) return;
    setMsg(null);
    try {
      await deleteAdminTag(csrf, t.id);
      setMsg({ kind: "success", text: `标签「${t.name}」已删除。` });
      load();
    } catch (err: any) {
      setMsg({ kind: "error", text: err.message || "删除失败。" });
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
                <label>标签名 <span style={{ color: "var(--admin-danger)" }}>*</span></label>
                <input
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="例如：typescript / react / backend"
                  maxLength={64}
                  required
                />
                <div className="admin-form-hint">最多 64 字符，建议用小写 + 短横线</div>
              </div>
              <div className="admin-form-group" style={{ justifyContent: "flex-end" }}>
                <label>&nbsp;</label>
                <button type="submit" className="admin-btn admin-btn-primary" disabled={submitting}>
                  {submitting ? "创建中…" : "创建标签"}
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
    </div>
  );
}
