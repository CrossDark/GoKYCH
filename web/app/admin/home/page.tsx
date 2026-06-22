"use client";

import { useState, useEffect } from "react";
import { getCsrf, getAdminHome, addSubsiteLink, deleteSubsiteLink, addFeatured, deleteFeatured } from "@/lib/api";
import type { SubsiteLink, FeaturedArticle } from "@/lib/types";

export default function AdminHome() {
  const [csrf, setCsrf] = useState("");
  const [links, setLinks] = useState<SubsiteLink[]>([]);
  const [featured, setFeatured] = useState<FeaturedArticle[]>([]);
  const [msg, setMsg] = useState("");
  const [linkForm, setLinkForm] = useState({ name: "", url: "", description: "", sort_order: 0 });
  const [featuredId, setFeaturedId] = useState("");

  const load = () => {
    if (!csrf) return;
    getAdminHome(csrf).then((d) => {
      setLinks(d.subsite_links);
      setFeatured(d.featured_articles);
    }).catch(() => {});
  };

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getAdminHome(r.csrf_token).then((d) => {
        setLinks(d.subsite_links);
        setFeatured(d.featured_articles);
      }).catch(() => {});
    });
  }, []);

  const handleAddLink = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg("");
    try {
      await addSubsiteLink(csrf, linkForm);
      setMsg("链接已添加。");
      setLinkForm({ name: "", url: "", description: "", sort_order: 0 });
      load();
    } catch (err: any) {
      setMsg(err.message || "添加失败。");
    }
  };

  const handleDeleteLink = async (id: number) => {
    try {
      await deleteSubsiteLink(csrf, id);
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  const handleAddFeatured = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg("");
    const id = parseInt(featuredId, 10);
    if (!id) { setMsg("请输入有效的文章 ID。"); return; }
    try {
      await addFeatured(csrf, id);
      setMsg("推荐文章已添加。");
      setFeaturedId("");
      load();
    } catch (err: any) {
      setMsg(err.message || "添加失败。");
    }
  };

  const handleDeleteFeatured = async (id: number) => {
    try {
      await deleteFeatured(csrf, id);
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  return (
    <div className="admin-home">
      <h1>首页管理</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      {/* Subsite links */}
      <section className="admin-section">
        <h2>子站点链接</h2>
        <details className="admin-form-section">
          <summary>添加链接</summary>
          <form onSubmit={handleAddLink} className="admin-form">
            <label>名称 <input value={linkForm.name} onChange={(e) => setLinkForm({ ...linkForm, name: e.target.value })} required /></label>
            <label>URL <input value={linkForm.url} onChange={(e) => setLinkForm({ ...linkForm, url: e.target.value })} required /></label>
            <label>描述 <input value={linkForm.description} onChange={(e) => setLinkForm({ ...linkForm, description: e.target.value })} /></label>
            <button type="submit" className="btn btn-primary">添加</button>
          </form>
        </details>
        {links.length === 0 ? (
          <p className="empty-message">暂无子站点链接。</p>
        ) : (
          <table className="admin-table">
            <thead><tr><th>名称</th><th>URL</th><th>操作</th></tr></thead>
            <tbody>
              {links.map((l: any) => (
                <tr key={l.id}>
                  <td>{l.name}</td>
                  <td>{l.url}</td>
                  <td><button className="btn btn-small btn-danger" onClick={() => handleDeleteLink(l.id)}>删除</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {/* Featured articles */}
      <section className="admin-section">
        <h2>推荐文章</h2>
        <details className="admin-form-section">
          <summary>添加推荐</summary>
          <form onSubmit={handleAddFeatured} className="admin-form">
            <label>文章 ID <input type="number" value={featuredId} onChange={(e) => setFeaturedId(e.target.value)} required /></label>
            <button type="submit" className="btn btn-primary">添加</button>
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
                  <td>{f.id}</td>
                  <td>{f.title}</td>
                  <td><span className="article-type-badge">{f.type}</span></td>
                  <td><button className="btn btn-small btn-danger" onClick={() => handleDeleteFeatured(f.id)}>移除</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
