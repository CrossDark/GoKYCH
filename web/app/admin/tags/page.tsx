"use client";

import { useEffect, useState } from "react";
import { getCsrf, listAdminTags, createTag, renameTag, deleteAdminTag } from "@/lib/api";
import type { AdminTag } from "@/lib/types";

export default function AdminTags() {
  const [csrf, setCsrf] = useState("");
  const [tags, setTags] = useState<AdminTag[]>([]);
  const [loading, setLoading] = useState(true);
  const [msg, setMsg] = useState("");
  const [newName, setNewName] = useState("");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState("");

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
    setMsg("");
    try {
      const r = await createTag(csrf, name);
      setMsg(r.existed ? `标签「${name}」已存在。` : `标签「${name}」已创建。`);
      setNewName("");
      load();
    } catch (err: any) {
      setMsg(err.message || "创建失败。");
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
    setMsg("");
    try {
      await renameTag(csrf, id, name);
      setMsg(`标签已重命名。`);
      cancelRename();
      load();
    } catch (err: any) {
      setMsg(err.message || "重命名失败。");
    }
  };

  const handleDelete = async (t: AdminTag) => {
    if (!confirm(`确定删除标签「${t.name}」吗？这会从所有引用此标签的文章上解除关联。`)) return;
    setMsg("");
    try {
      await deleteAdminTag(csrf, t.id);
      setMsg(`标签「${t.name}」已删除。`);
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  return (
    <div className="admin-tags">
      <h1>标签管理</h1>
      <p className="admin-hint">
        在此可统一管理全站标签：新建、重命名、删除。删除标签会同时清除其与文章的关联。
      </p>

      {msg && <p className="admin-msg">{msg}</p>}

      <form onSubmit={handleCreate} className="admin-form admin-tag-create">
        <label>
          新建标签
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="标签名（最多 64 字符）"
            maxLength={64}
            required
          />
        </label>
        <div className="admin-form-actions">
          <button type="submit" className="btn btn-primary">创建</button>
        </div>
      </form>

      {loading ? (
        <p className="loading">加载中…</p>
      ) : tags.length === 0 ? (
        <p className="empty-message">暂无标签。可以通过上方表单新建，或在编辑文章时勾选/新增标签。</p>
      ) : (
        <table className="admin-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>关联文章</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {tags.map((t) => (
              <tr key={t.id}>
                <td>
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
                    />
                  ) : (
                    <span
                      className="admin-tag-name"
                      onDoubleClick={() => startRename(t)}
                      title="双击重命名"
                    >
                      {t.name}
                    </span>
                  )}
                </td>
                <td>{t.count}</td>
                <td>
                  {editingId === t.id ? (
                    <>
                      <button className="btn btn-small btn-primary" onClick={() => commitRename(t.id)}>保存</button>
                      <button className="btn btn-small" onClick={cancelRename}>取消</button>
                    </>
                  ) : (
                    <>
                      <button className="btn btn-small" onClick={() => startRename(t)}>重命名</button>
                      <button className="btn btn-small btn-danger" onClick={() => handleDelete(t)} disabled={t.count > 0 && false /* allow delete even if used; only block on counts === 0 special case */}>
                        删除
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}