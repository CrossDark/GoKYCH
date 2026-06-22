"use client";

import { useState, useEffect, useCallback } from "react";
import { getCsrf, getAdminHome, addSubsiteLink, deleteSubsiteLink, addFeatured, deleteFeatured, listArticles } from "@/lib/api";
import type { SubsiteLink, FeaturedArticle, Article } from "@/lib/types";

export default function AdminHome() {
  const [csrf, setCsrf] = useState("");
  const [links, setLinks] = useState<SubsiteLink[]>([]);
  const [featured, setFeatured] = useState<FeaturedArticle[]>([]);
  const [msg, setMsg] = useState("");
  const [msgType, setMsgType] = useState<"success" | "error">("success");
  const [linkForm, setLinkForm] = useState({ name: "", url: "", description: "", sort_order: 0 });
  const [editingLink, setEditingLink] = useState<number | null>(null);
  const [editLinkForm, setEditLinkForm] = useState({ name: "", url: "", description: "", sort_order: 0 });

  // Article search for featured
  const [articleSearch, setArticleSearch] = useState("");
  const [articleSearchType, setArticleSearchType] = useState("");
  const [articleResults, setArticleResults] = useState<Article[]>([]);
  const [searching, setSearching] = useState(false);
  const [selectedArticle, setSelectedArticle] = useState<Article | null>(null);
  const [showArticleDropdown, setShowArticleDropdown] = useState(false);

  const showMsg = (m: string, t: "success" | "error" = "success") => {
    setMsg(m); setMsgType(t);
    setTimeout(() => setMsg(""), 4000);
  };

  const load = useCallback(() => {
    if (!csrf) return;
    getAdminHome(csrf).then((d) => {
      setLinks(d.subsite_links);
      setFeatured(d.featured_articles);
    }).catch(() => {});
  }, [csrf]);

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getAdminHome(r.csrf_token).then((d) => {
        setLinks(d.subsite_links);
        setFeatured(d.featured_articles);
      }).catch(() => {});
    });
  }, []);

  // Search articles
  const searchArticles = useCallback(async (q: string, type: string) => {
    if (!q.trim()) { setArticleResults([]); setShowArticleDropdown(false); return; }
    setSearching(true);
    try {
      const res = await listArticles(type || undefined, 1);
      const filtered = res.articles.filter(
        (a) => a.title.toLowerCase().includes(q.toLowerCase()) ||
               a.slug.toLowerCase().includes(q.toLowerCase())
      ).slice(0, 15);
      setArticleResults(filtered);
      setShowArticleDropdown(filtered.length > 0);
    } catch { setArticleResults([]); } finally { setSearching(false); }
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => searchArticles(articleSearch, articleSearchType), 300);
    return () => clearTimeout(timer);
  }, [articleSearch, articleSearchType, searchArticles]);

  const selectArticle = (a: Article) => {
    setSelectedArticle(a);
    setArticleSearch(`${a.title} (${a.type}/${a.slug})`);
    setShowArticleDropdown(false);
  };

  // ── Links ──
  const handleAddLink = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await addSubsiteLink(csrf, linkForm);
      showMsg("链接已添加。");
      setLinkForm({ name: "", url: "", description: "", sort_order: 0 });
      load();
    } catch (err: any) { showMsg(err.message || "添加失败。", "error"); }
  };

  const handleDeleteLink = async (id: number) => {
    try { await deleteSubsiteLink(csrf, id); load(); showMsg("已删除。"); }
    catch (err: any) { showMsg(err.message || "删除失败。", "error"); }
  };

  // ── Featured ──
  const handleAddFeatured = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedArticle) { showMsg("请选择一篇文章。", "error"); return; }
    try {
      await addFeatured(csrf, selectedArticle.id);
      showMsg("推荐文章已添加。");
      setSelectedArticle(null);
      setArticleSearch("");
      setArticleResults([]);
      load();
    } catch (err: any) { showMsg(err.message || "添加失败。", "error"); }
  };

  const handleDeleteFeatured = async (id: number) => {
    try { await deleteFeatured(csrf, id); load(); showMsg("已移除。"); }
    catch (err: any) { showMsg(err.message || "移除失败。", "error"); }
  };

  return (
    <div className="admin-home">
      <h1>首页管理</h1>
      {msg && <div className={`admin-msg ${msgType}`}>{msg}</div>}

      {/* ═══ Subsite Links ═══ */}
      <section className="admin-card">
        <div className="admin-card-header">
          <h2>🔗 子站点链接</h2>
          <span className="admin-hint">— 显示在导航栏中间区域</span>
        </div>
        <div className="admin-card-body">
          <details className="admin-inline-form">
            <summary className="btn btn-small">+ 添加链接</summary>
            <form onSubmit={handleAddLink} className="admin-form-row">
              <input placeholder="名称" value={linkForm.name} onChange={(e) => setLinkForm({ ...linkForm, name: e.target.value })} required />
              <input placeholder="URL (如 https://...)" value={linkForm.url} onChange={(e) => setLinkForm({ ...linkForm, url: e.target.value })} required />
              <input placeholder="描述（可选）" value={linkForm.description} onChange={(e) => setLinkForm({ ...linkForm, description: e.target.value })} />
              <button type="submit" className="btn btn-primary btn-small">添加</button>
            </form>
          </details>
          {links.length === 0 ? (
            <p className="empty-message">暂无子站点链接，添加后将在导航栏显示。</p>
          ) : (
            <table className="admin-table">
              <thead><tr><th>名称</th><th>URL</th><th>排序</th><th>操作</th></tr></thead>
              <tbody>
                {links.map((l: any) => (
                  <tr key={l.id}>
                    <td className="col-title">{l.name}</td>
                    <td className="col-url"><a href={l.url} target="_blank" rel="noopener">{l.url.length > 40 ? l.url.slice(0, 40) + "…" : l.url}</a></td>
                    <td className="col-sort">{l.sort_order ?? 0}</td>
                    <td className="col-actions">
                      <button className="btn btn-small btn-danger" onClick={() => handleDeleteLink(l.id)}>删除</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      {/* ═══ Featured Articles ═══ */}
      <section className="admin-card">
        <div className="admin-card-header">
          <h2>⭐ 推荐文章</h2>
          <span className="admin-hint">— 显示在首页推荐区域</span>
        </div>
        <div className="admin-card-body">
          <details className="admin-inline-form">
            <summary className="btn btn-small">+ 添加推荐</summary>
            <form onSubmit={handleAddFeatured} className="admin-form">
              <label>搜索文章</label>
              <div className="article-selector">
                <div className="article-selector-controls">
                  <select
                    value={articleSearchType}
                    onChange={(e) => setArticleSearchType(e.target.value)}
                    className="select-small"
                  >
                    <option value="">全部类型</option>
                    <option value="md">Markdown</option>
                    <option value="wikidot">Wikidot</option>
                    <option value="html">HTML</option>
                    <option value="bbcode">BBCode</option>
                    <option value="typst">Typst</option>
                  </select>
                  <div className="article-search-wrap">
                    <input
                      type="text"
                      placeholder="搜索文章标题或 slug…"
                      value={articleSearch}
                      onChange={(e) => { setArticleSearch(e.target.value); if (!e.target.value) { setSelectedArticle(null); setShowArticleDropdown(false); } }}
                      onFocus={() => { if (articleResults.length > 0) setShowArticleDropdown(true); }}
                      onBlur={() => setTimeout(() => setShowArticleDropdown(false), 200)}
                      className="article-search-input"
                    />
                    {showArticleDropdown && articleResults.length > 0 && (
                      <ul className="article-dropdown">
                        {articleResults.map((a) => (
                          <li
                            key={a.id}
                            className="article-dropdown-item"
                            onMouseDown={() => selectArticle(a)}
                          >
                            <span className="article-type-badge">{a.type}</span>
                            <span className="article-dropdown-title">{a.title}</span>
                            <span className="article-dropdown-slug">{a.slug}</span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                </div>
                {selectedArticle && (
                  <div className="article-selected">
                    ✅ 已选择：<strong>{selectedArticle.title}</strong>
                    <span className="article-type-badge">{selectedArticle.type}</span>
                    <code>{selectedArticle.slug}</code>
                    <span className="article-selected-id">ID: {selectedArticle.id}</span>
                  </div>
                )}
              </div>
              <button type="submit" className="btn btn-primary btn-small">添加推荐</button>
            </form>
          </details>
          {featured.length === 0 ? (
            <p className="empty-message">暂无推荐文章。</p>
          ) : (
            <table className="admin-table">
              <thead><tr><th>ID</th><th>标题</th><th>类型</th><th>操作</th></tr></thead>
              <tbody>
                {featured.map((f: any) => (
                  <tr key={f.id}>
                    <td>{f.article_id ?? f.id}</td>
                    <td className="col-title">{f.title}</td>
                    <td><span className="article-type-badge">{f.type || "—"}</span></td>
                    <td className="col-actions">
                      <button className="btn btn-small btn-danger" onClick={() => handleDeleteFeatured(f.id)}>移除</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>
    </div>
  );
}
