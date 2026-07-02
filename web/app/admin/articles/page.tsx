"use client";

import { useState, useEffect, Suspense } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import { getCsrf, getMe, listArticles, getArticle, createArticle, updateArticle, deleteArticle } from "@/lib/api";
import type { Article, ArticleListResult, User } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { MarkdownEditor } from "@/components/admin/MarkdownEditor";
import { fmtDate } from "@/lib/format";

const TYPES = [
  { key: "md", label: "Markdown" },
  { key: "wikidot", label: "Wikidot" },
  { key: "html", label: "HTML" },
  { key: "bbcode", label: "BBCode" },
  { key: "typst", label: "Typst" },
] as const;

function TypeBadge({ type }: { type: string }) {
  const cls =
    type === "md" ? "primary" :
    type === "wikidot" ? "danger" :
    type === "html" ? "warning" :
    type === "bbcode" ? "success" :
    "neutral";
  return <span className={`admin-badge admin-badge-${cls}`}>{type}</span>;
}

function AdminArticlesInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const toast = useToast();
  const [csrf, setCsrf] = useState("");
  const [me, setMe] = useState<User | null>(null);
  const [result, setResult] = useState<ArticleListResult | null>(null);
  const [page, setPage] = useState(1);
  const [filterType, setFilterType] = useState("");
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Article | null>(null);
  const [form, setForm] = useState({ type: "md", slug: "", title: "", content: "", tags: "" });
  const [initialEditLoaded, setInitialEditLoaded] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<Article | null>(null);
  const isDirty = !!(form.title || form.content || form.slug || form.tags);

  // Regular users see only their own articles; admin/owner see everything.
  // The list label reflects that so the page reads as "我的文章" for the
  // regular user and "文章管理" for the admin (label flip is local; the
  // URL stays /admin/articles either way).
  const isRegular = me?.role === "user";
  const myAuthorId = isRegular ? me?.id : undefined;
  const pageTitle = isRegular ? "我的文章" : "文章管理";

  const load = (p: number, t: string) => {
    listArticles(t || undefined, p, myAuthorId).then(setResult).catch(() => {});
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
    });
    getMe().then((r) => {
      if (r.user) setMe(r.user);
    }).catch(() => {});
  }, []);

  // Re-load when the author filter resolves (i.e. after /me returns) — the
  // first load uses myAuthorId=undefined which would over-fetch for
  // regular users.
  useEffect(() => {
    if (isRegular && myAuthorId === undefined) return;
    load(page, filterType);
  }, [page, filterType, myAuthorId, isRegular]);

  // Read ?type= from URL (dashboard links)
  useEffect(() => {
    const t = searchParams.get("type");
    if (t && TYPES.find(x => x.key === t)) setFilterType(t);
  }, [searchParams]);

  // Handle direct edit link from article page
  useEffect(() => {
    if (initialEditLoaded || !csrf) return;
    const editType = searchParams.get("editType");
    const editSlug = searchParams.get("editSlug");
    if (editType && editSlug) {
      getArticle(editType, editSlug).then((detail) => {
        const a = detail.article;
        setEditing(a);
        setForm({ type: a.type, slug: a.slug, title: a.title, content: a.content, tags: (a.tags || []).join(", ") });
        setFilterType(a.type);
        setPage(1);
        setInitialEditLoaded(true);
        setTimeout(() => {
          const details = document.querySelector(".admin-form-section") as HTMLDetailsElement;
          if (details) details.open = true;
        }, 100);
        router.replace("/admin/articles");
      }).catch(() => {});
    }
    setInitialEditLoaded(true);
  }, [csrf, searchParams, initialEditLoaded, router]);

  const handleCreate = () => {
    setEditing(null);
    setForm({ type: "md", slug: "", title: "", content: "", tags: "" });
  };

  const handleEdit = (a: Article) => {
    setEditing(a);
    setForm({ type: a.type, slug: a.slug, title: a.title, content: a.content, tags: (a.tags || []).join(", ") });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    const tags = form.tags.split(",").map((t) => t.trim()).filter(Boolean);
    try {
      if (editing) {
        await updateArticle(editing.type, editing.slug, csrf, { title: form.title, content: form.content, tags });
        toast.success("文章已更新。");
      } else {
        await createArticle(form.type, csrf, { slug: form.slug, title: form.title, content: form.content, tags });
        toast.success("文章已创建。");
      }
      load(page, filterType);
      setEditing(null);
      setForm({ type: "md", slug: "", title: "", content: "", tags: "" });
    } catch (err: any) {
      toast.error(err.message || "操作失败。");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = (a: Article) => setPendingDelete(a);
  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const target = pendingDelete;
    try {
      await deleteArticle(target.type, target.slug, csrf);
      toast.success(`文章「${target.title}」已删除。`);
      load(page, filterType);
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err; // keep modal open so the user can retry or cancel
    } finally {
      setPendingDelete(null);
    }
  };

  // Client-side filter (search in title/slug/tags)
  const filtered = result?.articles?.filter((a) => {
    if (!search) return true;
    const s = search.toLowerCase();
    return a.title.toLowerCase().includes(s)
      || a.slug.toLowerCase().includes(s)
      || (a.tags || []).some((t) => t.toLowerCase().includes(s));
  }) ?? [];

  // Warn before leaving with unsaved form changes.
  useBeforeUnload(isDirty && !submitting);

  return (
    <div className="admin-articles">
      <div className="admin-page-header">
        <div>
          <h1>{pageTitle}</h1>
          <div className="admin-page-subtitle">
            {result ? `共 ${result.total} 篇` : "加载中…"}
          </div>
        </div>
        <div className="admin-header-actions">
          <button className="admin-btn admin-btn-primary" onClick={handleCreate}>
            ＋ 新建文章
          </button>
        </div>
      </div>

      {/* New/Edit form */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>{editing ? `✏️ 编辑：${editing.title}` : "＋ 新建文章"}</h2>
          {editing && (
            <button className="admin-btn admin-btn-ghost admin-btn-sm" onClick={handleCreate}>取消编辑</button>
          )}
        </div>
        <div className="admin-card-body">
          <form onSubmit={handleSubmit} className="admin-form">
            {!editing && (
              <div className="admin-form-row">
                <div className="admin-form-group">
                  <label htmlFor="article-type">类型</label>
                  <select
                    id="article-type"
                    value={form.type}
                    onChange={(e) => setForm({ ...form, type: e.target.value })}
                  >
                    {TYPES.map((t) => <option key={t.key} value={t.key}>{t.label}</option>)}
                  </select>
                </div>
                <div className="admin-form-group">
                  <label htmlFor="article-slug">
                    Slug
                    <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
                  </label>
                  <input
                    id="article-slug"
                    value={form.slug}
                    onChange={(e) => setForm({ ...form, slug: e.target.value })}
                    placeholder="my-first-note / 我的第一篇笔记"
                    required
                    aria-describedby="article-slug-hint"
                  />
                  <div id="article-slug-hint" className="admin-form-hint">
                    URL 中的标识符，支持任意 Unicode 字符，不能包含 / 或 \
                  </div>
                </div>
              </div>
            )}
            <div className="admin-form-group">
              <label htmlFor="article-title">
                标题
                <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
              </label>
              <input
                id="article-title"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                required
              />
            </div>
            <div className="admin-form-group">
              <label htmlFor="article-content">
                内容
                <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
              </label>
              <MarkdownEditor
                id="article-content"
                value={form.content}
                onChange={(next) => setForm({ ...form, content: next })}
                type={form.type}
                rows={12}
                required
                placeholder="支持 Markdown / Wikidot / HTML / BBCode / Typst（取决于类型）"
              />
            </div>
            <div className="admin-form-group">
              <label htmlFor="article-tags">
                标签 <span style={{ fontWeight: 400, color: "var(--text-muted)" }}>（逗号分隔）</span>
              </label>
              <input
                id="article-tags"
                value={form.tags}
                onChange={(e) => setForm({ ...form, tags: e.target.value })}
                placeholder="tag1, tag2, tag3"
              />
            </div>
            <div className="admin-form-actions">
              <button
                type="submit"
                className={`admin-btn admin-btn-primary ${submitting ? "admin-btn-loading" : ""}`}
                disabled={submitting}
              >
                {editing ? "保存修改" : "创建文章"}
              </button>
              {editing && (
                <button type="button" className="admin-btn admin-btn-ghost" onClick={handleCreate}>
                  取消
                </button>
              )}
              <div className="admin-form-actions-spacer" />
              {editing && (
                <span style={{ fontSize: "0.78rem", color: "var(--text-muted)" }}>
                  类型 / Slug 在编辑模式下不可修改
                </span>
              )}
            </div>
          </form>
        </div>
      </div>

      {/* Filter bar */}
      <div className="admin-filter">
        <span className="admin-filter-label">类型：</span>
        <button
          className={`admin-filter-chip ${filterType === "" ? "active" : ""}`}
          onClick={() => { setFilterType(""); setPage(1); }}
        >
          全部
        </button>
        {TYPES.map((t) => (
          <button
            key={t.key}
            className={`admin-filter-chip ${filterType === t.key ? "active" : ""}`}
            onClick={() => { setFilterType(t.key); setPage(1); }}
          >
            {t.label}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <div className="admin-search-box" style={{ maxWidth: 240 }}>
          <span className="admin-search-box-icon">🔍</span>
          <input
            placeholder="搜索标题 / Slug / 标签"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* List */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>
            📋 文章列表
            {filterType && <span style={{ fontWeight: 400, color: "var(--text-muted)", fontSize: "0.85rem", marginLeft: "0.5rem" }}>· {TYPES.find(t => t.key === filterType)?.label}</span>}
          </h2>
          <div className="admin-card-actions">
            <span style={{ fontSize: "0.82rem", color: "var(--text-muted)" }}>
              {search ? `匹配 ${filtered.length} / ${result?.articles.length ?? 0}` : `共 ${result?.total ?? 0} 篇`}
            </span>
          </div>
        </div>
        <div className="admin-card-body no-padding">
          {!result ? (
            <div className="admin-empty">
              <div className="admin-skeleton" style={{ height: 24, marginBottom: 8 }}>—</div>
              <div className="admin-skeleton" style={{ height: 24, marginBottom: 8 }}>—</div>
              <div className="admin-skeleton" style={{ height: 24 }}>—</div>
            </div>
          ) : filtered.length === 0 ? (
            <div className="admin-empty">
              <span className="admin-empty-icon">{search ? "🔍" : "📝"}</span>
              <div className="admin-empty-title">{search ? "没有匹配的文章" : "还没有文章"}</div>
              <div>{search ? "试试调整搜索词" : <>点击右上角 <strong>新建文章</strong> 开始</>}</div>
            </div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>类型</th>
                  <th>标题</th>
                  <th>Slug</th>
                  <th>标签</th>
                  <th className="col-date">更新于</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((a) => (
                  <tr key={a.id}>
                    <td><TypeBadge type={a.type} /></td>
                    <td className="col-title">
                      <Link href={`/${a.type}/${a.slug}`} target="_blank" style={{ color: "inherit" }}>
                        {a.title}
                      </Link>
                    </td>
                    <td className="col-slug">{a.slug}</td>
                    <td>
                      <div style={{ display: "flex", flexWrap: "wrap", gap: "0.2rem" }}>
                        {(a.tags || []).map((t) => (
                          <Link key={t} href={`/labels/${t}`} target="_blank" className="admin-badge admin-badge-neutral">
                            {t}
                          </Link>
                        ))}
                      </div>
                    </td>
                    <td className="col-date">{fmtDate(a.updated_at)}</td>
                    <td className="col-actions">
                      <Link href={`/${a.type}/${a.slug}`} target="_blank" className="admin-btn admin-btn-outline admin-btn-sm" title="查看">👁</Link>
                      <Link
                        href={`/admin/articles/${a.type}/${a.slug}`}
                        className="admin-btn admin-btn-secondary admin-btn-sm"
                        title="编辑"
                      >
                        ✏️
                      </Link>
                      <button className="admin-btn admin-btn-danger admin-btn-sm" onClick={() => handleDelete(a)} title="删除">🗑</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {/* Pagination */}
        {result && result.total_pages > 1 && (
          <div className="admin-card-footer">
            <span>第 {page} / {result.total_pages} 页 · 共 {result.total} 条</span>
            <div className="admin-pagination-controls">
              <button
                className="admin-pagination-btn"
                onClick={() => setPage(Math.max(1, page - 1))}
                disabled={page === 1}
              >
                ←
              </button>
              {Array.from({ length: result.total_pages }, (_, i) => i + 1)
                .filter((p) => p === 1 || p === result.total_pages || Math.abs(p - page) <= 2)
                .reduce<(number | "…")[]>((acc, p, i, arr) => {
                  if (i > 0 && (p as number) - (arr[i - 1] as number) > 1) acc.push("…");
                  acc.push(p);
                  return acc;
                }, [])
                .map((item, i) =>
                  item === "…" ? (
                    <span key={`e${i}`} className="admin-pagination-ellipsis">…</span>
                  ) : (
                    <button
                      key={item}
                      className={`admin-pagination-btn ${page === item ? "active" : ""}`}
                      onClick={() => setPage(item)}
                    >
                      {item}
                    </button>
                  )
                )}
              <button
                className="admin-pagination-btn"
                onClick={() => setPage(Math.min(result.total_pages, page + 1))}
                disabled={page === result.total_pages}
              >
                →
              </button>
            </div>
          </div>
        )}
      </div>

      <AdminConfirm
        open={!!pendingDelete}
        title="删除文章"
        message={pendingDelete ? `确定要删除「${pendingDelete.title}」吗？此操作不可撤销。` : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}

export default function AdminArticles() {
  return (
    <Suspense fallback={<div className="admin-articles"><p>加载中…</p></div>}>
      <AdminArticlesInner />
    </Suspense>
  );
}
