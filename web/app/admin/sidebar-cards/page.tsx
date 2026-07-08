"use client";

// Admin: Sidebar cards
//
// Site-level navigation cards rendered inside the front-end <SideDrawer>
// (the ☰ drawer to the LEFT of the site name). This admin page lets
// site admins / owners create / reorder / edit / hide / delete cards.
//
// UX notes:
//   - "Cards" ≠ "Article Tags" (the labels page). The two drawers in
//     the header serve different intents: tags = content index by
//     label, cards = curated site navigation. Don't merge the two
//     tables even though their schema shape is similar.
//   - The is_active toggle is a *soft hide*, not a delete; cards
//     that were once visible can be re-shown without re-creating.
//     The trash button does a hard delete.
//   - sort_order is admin-controlled. New cards without an explicit
//     order land at MAX(existing)+100 on the server, which keeps
//     them visually last without forcing the form to know about
//     ordering.
//   - URL is validated upstream (looksLikeReasonableURL) — javascript:
//     / data: / etc. all 400. The form mirrors that allow-list in
//     its hint text.

import { useEffect, useState } from "react";
import {
  getCsrf,
  listAdminSidebarCards,
  createSidebarCard,
  updateSidebarCard,
  deleteAdminSidebarCard,
} from "@/lib/api";
import type { AdminSidebarCard } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";

const EMPTY_FORM: Omit<AdminSidebarCard, "id"> = {
  title: "",
  url: "/",
  icon: "",
  description: "",
  sort_order: 0,
  is_external: false,
  is_active: true,
};

export default function AdminSidebarCards() {
  const [csrf, setCsrf] = useState("");
  const [cards, setCards] = useState<AdminSidebarCard[]>([]);
  const [loading, setLoading] = useState(true);
  const toast = useToast();
  const [search, setSearch] = useState("");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<Omit<AdminSidebarCard, "id">>(EMPTY_FORM);
  const [submitting, setSubmitting] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<AdminSidebarCard | null>(null);

  const load = () => {
    if (!csrf) return;
    setLoading(true);
    listAdminSidebarCards(csrf)
      .then(setCards)
      .catch((err) => {
        toast.error(err?.message || "加载失败。");
        setCards([]);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token));
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (csrf) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [csrf]);

  const startCreate = () => {
    setEditingId(null);
    // Empty form with is_active true so a freshly created card is
    // visible by default. The backend autofills sort_order to the
    // bottom so the form doesn't have to guess.
    setForm({ ...EMPTY_FORM });
  };

  const startEdit = (c: AdminSidebarCard) => {
    setEditingId(c.id);
    setForm({
      title: c.title,
      url: c.url,
      icon: c.icon,
      description: c.description,
      sort_order: c.sort_order,
      is_external: c.is_external,
      is_active: c.is_active,
    });
  };

  const cancelEdit = () => {
    setEditingId(null);
    setForm({ ...EMPTY_FORM });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const title = form.title.trim();
    const url = form.url.trim();
    if (!title) {
      toast.error("标题不能为空。");
      return;
    }
    if (!url) {
      toast.error("链接不能为空。");
      return;
    }
    setSubmitting(true);
    try {
      if (editingId != null) {
        await updateSidebarCard(csrf, editingId, { id: editingId, ...form });
        toast.success(`卡片「${title}」已更新。`);
      } else {
        await createSidebarCard(csrf, { id: 0, ...form });
        toast.success(`卡片「${title}」已创建。`);
      }
      cancelEdit();
      load();
    } catch (err: any) {
      toast.error(err?.message || "保存失败。");
    } finally {
      setSubmitting(false);
    }
  };

  const toggleActive = async (c: AdminSidebarCard) => {
    try {
      await updateSidebarCard(csrf, c.id, {
        id: c.id,
        ...{
          title: c.title,
          url: c.url,
          icon: c.icon,
          description: c.description,
          sort_order: c.sort_order,
          is_external: c.is_external,
        },
        is_active: !c.is_active,
      });
      toast.success(c.is_active ? `卡片「${c.title}」已隐藏。` : `卡片「${c.title}」已显示。`);
      load();
    } catch (err: any) {
      toast.error(err?.message || "状态切换失败。");
    }
  };

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const target = pendingDelete;
    try {
      await deleteAdminSidebarCard(csrf, target.id);
      toast.success(`卡片「${target.title}」已删除。`);
      load();
    } catch (err: any) {
      toast.error(err?.message || "删除失败。");
    } finally {
      setPendingDelete(null);
    }
  };

  const filtered = cards.filter((c) => {
    if (!search.trim()) return true;
    const q = search.toLowerCase();
    return (
      c.title.toLowerCase().includes(q) ||
      c.url.toLowerCase().includes(q) ||
      (c.description || "").toLowerCase().includes(q)
    );
  });

  return (
    <div className="admin-page">
      <div className="admin-page-header">
        <h1>侧栏卡片</h1>
        <div className="admin-page-subtitle">
          管理前端 <code>☰</code> 侧栏卡片,在 header 网站名称左侧点击展开。
          <br />
          与「<a href="/admin/tags">标签管理</a>」(<code>🏷️</code>) 是不同的概念 —
          卡片是站点导航入口,标签是文章索引。
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>卡片列表</h2>
          <div className="admin-card-actions">
            <input
              type="search"
              className="admin-input"
              placeholder="搜索标题/链接/描述…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{ minWidth: 220 }}
            />
            <button type="button" className="admin-btn admin-btn-primary" onClick={startCreate} disabled={loading}>
              + 新增卡片
            </button>
          </div>
        </div>

        {loading && cards.length === 0 ? (
          <div className="admin-card-body">
            <p className="admin-empty">加载中…</p>
          </div>
        ) : cards.length === 0 ? (
          <div className="admin-card-body">
            <p className="admin-empty">暂无卡片,点击右上「新增卡片」开始。</p>
          </div>
        ) : (
          <div className="admin-card-body no-padding">
            <table className="admin-table">
              <thead>
                <tr>
                  <th style={{ width: 56 }}>图标</th>
                  <th>标题 / 链接</th>
                  <th>描述</th>
                  <th style={{ width: 70 }}>顺序</th>
                  <th style={{ width: 90 }}>显示</th>
                  <th style={{ width: 90 }}>外链</th>
                  <th style={{ width: 180 }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((c) => (
                  <tr key={c.id}>
                    <td style={{ fontSize: "1.4em", textAlign: "center" }}>{c.icon || "·"}</td>
                    <td>
                      <div style={{ fontWeight: 600 }}>{c.title}</div>
                      <div className="admin-text-mono" style={{ fontSize: "0.85em" }}>
                        {c.url}
                      </div>
                    </td>
                    <td>
                      <span className="admin-text-muted">{c.description || "—"}</span>
                    </td>
                    <td>
                      <code>{c.sort_order}</code>
                    </td>
                    <td>
                      <label className="admin-switch">
                        <input
                          type="checkbox"
                          checked={c.is_active}
                          onChange={() => toggleActive(c)}
                        />
                        <span>{c.is_active ? "显示" : "隐藏"}</span>
                      </label>
                    </td>
                    <td>
                      {c.is_external ? (
                        <span className="admin-badge admin-badge-warn">外链</span>
                      ) : (
                        <span className="admin-badge">站内</span>
                      )}
                    </td>
                    <td>
                      <button
                        type="button"
                        className="admin-btn admin-btn-sm"
                        onClick={() => startEdit(c)}
                      >
                        编辑
                      </button>
                      {" "}
                      <button
                        type="button"
                        className="admin-btn admin-btn-sm admin-btn-danger"
                        onClick={() => setPendingDelete(c)}
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {filtered.length !== cards.length && (
              <div className="admin-card-footer">
                <span>共 {cards.length} 张卡片,{filtered.length} 张匹配搜索</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Edit / create panel — single-form, no modal library needed */}
      <div className="admin-card" style={{ marginTop: "1rem" }}>
        <div className="admin-card-header">
          <h2>{editingId != null ? "编辑卡片" : "新增卡片"}</h2>
          {editingId != null && (
            <button type="button" className="admin-btn admin-btn-sm" onClick={cancelEdit}>
              取消编辑
            </button>
          )}
        </div>
        <div className="admin-card-body">
          <form className="admin-form" onSubmit={handleSubmit}>
            <div className="admin-form-grid">
              <label className="admin-form-field">
                <span>标题 *</span>
                <input
                  type="text"
                  className="admin-input"
                  maxLength={64}
                  required
                  value={form.title}
                  onChange={(e) => setForm({ ...form, title: e.target.value })}
                  placeholder="例如：GitHub"
                />
              </label>

              <label className="admin-form-field">
                <span>图标 (emoji,可空)</span>
                <input
                  type="text"
                  className="admin-input"
                  maxLength={32}
                  value={form.icon}
                  onChange={(e) => setForm({ ...form, icon: e.target.value })}
                  placeholder="例如：🐱"
                />
              </label>

              <label className="admin-form-field" style={{ gridColumn: "1 / -1" }}>
                <span>链接 *</span>
                <input
                  type="text"
                  className="admin-input"
                  required
                  value={form.url}
                  onChange={(e) => setForm({ ...form, url: e.target.value })}
                  placeholder="/labels/foo 或 https://example.com (限 http(s)://, //, /, #, mailto:)"
                />
                <span className="admin-hint">
                  新建时若 <code>sort_order</code>=0,服务端自动追加为
                  <code>MAX+100</code>(添加到末尾)。
                </span>
              </label>

              <label className="admin-form-field" style={{ gridColumn: "1 / -1" }}>
                <span>描述 (可空)</span>
                <input
                  type="text"
                  className="admin-input"
                  maxLength={256}
                  value={form.description}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}
                  placeholder="鼠标悬停时显示,或者卡片副标题"
                />
              </label>

              <label className="admin-form-field">
                <span>顺序</span>
                <input
                  type="number"
                  className="admin-input"
                  value={form.sort_order}
                  onChange={(e) => setForm({ ...form, sort_order: parseInt(e.target.value, 10) || 0 })}
                />
                <span className="admin-hint">数字越小越靠前</span>
              </label>

              <div className="admin-form-field">
                <span>选项</span>
                <label className="admin-checkbox">
                  <input
                    type="checkbox"
                    checked={form.is_external}
                    onChange={(e) => setForm({ ...form, is_external: e.target.checked })}
                  />
                  <span>外链(target=_blank,rel=noopener)</span>
                </label>
                <label className="admin-checkbox">
                  <input
                    type="checkbox"
                    checked={form.is_active}
                    onChange={(e) => setForm({ ...form, is_active: e.target.checked })}
                  />
                  <span>显示(关闭则隐藏但不删除)</span>
                </label>
              </div>
            </div>

            <div className="admin-form-footer">
              <button
                type="submit"
                className="admin-btn admin-btn-primary"
                disabled={submitting}
              >
                {submitting ? "保存中…" : editingId != null ? "保存修改" : "创建卡片"}
              </button>
              {editingId != null && (
                <button type="button" className="admin-btn" onClick={cancelEdit}>
                  取消
                </button>
              )}
            </div>
          </form>
        </div>
      </div>

      <AdminConfirm
        open={!!pendingDelete}
        title="删除侧栏卡片"
        message={
          pendingDelete
            ? `确定要删除卡片「${pendingDelete.title}」吗？此操作不可撤销。`
            : ""
        }
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}
