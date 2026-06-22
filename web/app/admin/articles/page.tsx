"use client";

import { useState, useEffect, Suspense } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import { getCsrf, listArticles, getArticle, createArticle, updateArticle, deleteArticle } from "@/lib/api";
import type { Article, ArticleListResult } from "@/lib/types";

const TYPES = ["md", "wikidot", "html", "bbcode", "typst"];

function AdminArticlesInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const [csrf, setCsrf] = useState("");
  const [result, setResult] = useState<ArticleListResult | null>(null);
  const [page, setPage] = useState(1);
  const [filterType, setFilterType] = useState("");
  const [editing, setEditing] = useState<Article | null>(null);
  const [form, setForm] = useState({ type: "md", slug: "", title: "", content: "", tags: "" });
  const [msg, setMsg] = useState("");
  const [initialEditLoaded, setInitialEditLoaded] = useState(false);

  const load = (p: number, t: string) => {
    listArticles(t || undefined, p).then(setResult).catch(() => {});
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      load(1, "");
    });
  }, []);

  useEffect(() => { load(page, filterType); }, [page, filterType]);

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
        // Open the form section
        setTimeout(() => {
          const details = document.querySelector(".admin-form-section") as HTMLDetailsElement;
          if (details) details.open = true;
        }, 100);
        // Clean URL
        router.replace("/admin/articles");
      }).catch(() => {});
    }
    setInitialEditLoaded(true);
  }, [csrf, searchParams, initialEditLoaded, router]);

  const handleCreate = () => {
    setEditing(null);
    setForm({ type: "md", slug: "", title: "", content: "", tags: "" });
    setMsg("");
  };

  const handleEdit = (a: Article) => {
    setEditing(a);
    setForm({ type: a.type, slug: a.slug, title: a.title, content: a.content, tags: (a.tags || []).join(", ") });
    setMsg("");
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg("");
    const tags = form.tags.split(",").map((t) => t.trim()).filter(Boolean);
    try {
      if (editing) {
        await updateArticle(editing.type, editing.slug, csrf, { title: form.title, content: form.content, tags });
        setMsg("文章已更新。");
      } else {
        await createArticle(form.type, csrf, { slug: form.slug, title: form.title, content: form.content, tags });
        setMsg("文章已创建。");
      }
      load(page, filterType);
      setEditing(null);
      setForm({ type: "md", slug: "", title: "", content: "", tags: "" });
    } catch (err: any) {
      setMsg(err.message || "操作失败。");
    }
  };

  const handleDelete = async (a: Article) => {
    if (!confirm(`确定删除「${a.title}」吗？此操作不可撤销。`)) return;
    try {
      await deleteArticle(a.type, a.slug, csrf);
      setMsg("文章已删除。");
      load(page, filterType);
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  return (
    <div className="admin-articles">
      <h1>文章管理</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      {/* New/Edit form */}
      <details className="admin-form-section">
        <summary>{editing ? `编辑：${editing.title}` : "新建文章"}</summary>
        <form onSubmit={handleSubmit} className="admin-form">
          {!editing && (
            <>
              <label>类型
                <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
                  {TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
                </select>
              </label>
              <label>Slug
                <input value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} placeholder="hello-world" required />
              </label>
            </>
          )}
          <label>标题
            <input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} required />
          </label>
          <label>内容
            <textarea value={form.content} onChange={(e) => setForm({ ...form, content: e.target.value })} rows={8} required />
          </label>
          <label>标签（逗号分隔）
            <input value={form.tags} onChange={(e) => setForm({ ...form, tags: e.target.value })} />
          </label>
          <div className="admin-form-actions">
            <button type="submit" className="btn btn-primary">{editing ? "保存" : "创建"}</button>
            {editing && <button type="button" className="btn" onClick={handleCreate}>取消</button>}
          </div>
        </form>
      </details>

      {/* Filter */}
      <div className="admin-filter">
        <span>筛选类型：</span>
        {["", ...TYPES].map((t) => (
          <button key={t} className={`btn btn-small ${filterType === t ? "active" : ""}`} onClick={() => { setFilterType(t); setPage(1); }}>
            {t || "全部"}
          </button>
        ))}
      </div>

      {/* List */}
      {result && result.articles.length === 0 ? (
        <p className="empty-message">暂无文章。</p>
      ) : (
        <table className="admin-table">
          <thead>
            <tr>
              <th>类型</th><th>标题</th><th>Slug</th><th>标签</th><th>更新于</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {result?.articles.map((a) => (
              <tr key={a.id}>
                <td><span className="article-type-badge">{a.type}</span></td>
                <td><Link href={`/${a.type}/${a.slug}`} target="_blank">{a.title}</Link></td>
                <td>{a.slug}</td>
                <td>{(a.tags || []).join(", ")}</td>
                <td>{new Date(a.updated_at).toLocaleDateString("zh-CN")}</td>
                <td>
                  <button className="btn btn-small" onClick={() => handleEdit(a)}>编辑</button>
                  <button className="btn btn-small btn-danger" onClick={() => handleDelete(a)}>删除</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {/* Pagination */}
      {result && result.total_pages > 1 && (
        <div className="admin-pagination">
          {Array.from({ length: result.total_pages }, (_, i) => i + 1).map((p) => (
            <button key={p} className={`btn btn-small ${page === p ? "active" : ""}`} onClick={() => setPage(p)}>
              {p}
            </button>
          ))}
        </div>
      )}
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
