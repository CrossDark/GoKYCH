"use client";

import { useState, useEffect, useRef, useLayoutEffect } from "react";
import Link from "next/link";
import type { ArticleDetail, Comment } from "@/lib/types";
import { RatingWidget } from "./RatingWidget";
import { CommentSection } from "./CommentSection";
import { getMe, getCsrf, addLineComment } from "@/lib/api";

interface Props {
  data: ArticleDetail;
  articleType: string;
  articleSlug: string;
}

export function ArticleView({ data, articleType, articleSlug }: Props) {
  const { article, html, rating, comments: rawComments, line_comment_counts: rawLineCounts, can_edit } = data;
  const comments = rawComments ?? [];
  const contentRef = useRef<HTMLDivElement>(null);
  const lineCountsRef = useRef<Record<number, number>>({});
  const lineDataRef = useRef<Record<number, Comment[]>>({});

  const [totalLines, setTotalLines] = useState(0);
  const [lineCounts, setLineCounts] = useState<Record<number, number>>(rawLineCounts ?? {});
  const [isLoggedIn, setIsLoggedIn] = useState(false);
  const [currentUser, setCurrentUser] = useState<{ nickname?: string; username: string } | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [panelOpen, setPanelOpen] = useState(true);
  const [guideOpen, setGuideOpen] = useState(false);

  const [popup, setPopup] = useState<{ lineNum: number; x: number; y: number } | null>(null);
  const [popupInput, setPopupInput] = useState("");
  const [popupComments, setPopupComments] = useState<Comment[]>([]);

  // Auth
  useEffect(() => {
    getMe().then((r) => {
      if (r.user) { setIsLoggedIn(true); setCurrentUser(r.user); getCsrf().then((c) => setCsrfToken(c.csrf_token)).catch(() => {}); }
    }).catch(() => {});
  }, []);

  // Load line comments
  useEffect(() => {
    setLineCounts(rawLineCounts ?? {});
    lineCountsRef.current = rawLineCounts ?? {};
    const allComments = (data as any).line_comments ?? [];
    const d: Record<number, Comment[]> = {};
    allComments.forEach((c: Comment) => { const ln = (c as any).line_number; if (!d[ln]) d[ln] = []; d[ln].push(c); });
    lineDataRef.current = d;
  }, [data]);

  // Assign line numbers + markers directly in DOM after every render
  useLayoutEffect(() => {
    const container = contentRef.current;
    if (!container) return;

    // Assign line numbers to block elements
    container.querySelectorAll("[data-line]").forEach(el => el.removeAttribute("data-line"));
    const blocks = container.querySelectorAll("p, h1, h2, h3, h4, h5, h6, li, pre, blockquote, div, table");
    let ln = 0;
    blocks.forEach((block) => {
      const el = block as HTMLElement;
      if (el.closest("pre") && el.tagName !== "PRE") return;
      if (el.tagName !== "LI" && el.closest("li") && el.closest("li") !== el) return;
      if (el.tagName !== "TABLE" && el.closest("table")) return;
      ln++;
      el.setAttribute("data-line", String(ln));
    });
    setTotalLines(ln);

    // Apply comment markers
    const counts = lineCountsRef.current;
    const data = lineDataRef.current;
    container.querySelectorAll("[data-line]").forEach((block) => {
      const n = parseInt(block.getAttribute("data-line")!);
      const count = counts[n] || 0;
      const comments = data[n] || [];
      block.classList.toggle("has-line-comments", count > 0);
      if (count === 1 && comments.length === 1) {
        block.classList.add("single-comment-expanded");
        block.setAttribute("data-single-comment", comments[0].content);
      } else if (count > 1) {
        block.classList.add("single-comment-expanded");
        block.setAttribute("data-single-comment", `💬 ${count}条评论 — 点击展开`);
      } else {
        block.classList.remove("single-comment-expanded");
        block.removeAttribute("data-single-comment");
      }
    });
  });

  const openPopupForLine = (ln: number, rect: DOMRect) => {
    const comments = lineDataRef.current[ln] || [];
    setPopupComments(comments);
    setPopupInput("");
    setPopup({ lineNum: ln, x: Math.max(rect.left + rect.width + 8, 20), y: Math.min(Math.max(rect.top, 10), window.innerHeight - 340) });
  };

  const handleContentClick = (e: React.MouseEvent) => {
    const target = (e.target as HTMLElement).closest("[data-line]") as HTMLElement;
    if (!target || (e.target as HTMLElement).closest(".line-comments-container")) return;
    const ln = parseInt(target.getAttribute("data-line")!);
    if (!ln) return;
    contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active"));
    target.classList.add("line-active");
    openPopupForLine(ln, target.getBoundingClientRect());
  };

  const handleLineCommentSubmit = async () => {
    if (!popupInput.trim() || !popup || !csrfToken) return;
    try {
      const authorName = currentUser?.nickname || currentUser?.username || undefined;
      const result = await addLineComment(articleType, articleSlug, csrfToken, { line_number: popup.lineNum, content: popupInput.trim(), author_name: authorName });
      const newComment = (result as any).comment ?? result;
      const prev = lineDataRef.current;
      const u = { ...prev };
      if (!u[popup.lineNum]) u[popup.lineNum] = [];
      u[popup.lineNum] = [...u[popup.lineNum], newComment];
      lineDataRef.current = u;
      lineCountsRef.current = { ...lineCountsRef.current, [popup.lineNum]: (lineCountsRef.current[popup.lineNum] || 0) + 1 };
      setLineCounts({ ...lineCountsRef.current });
      setPopupComments((prev) => [...prev, newComment]);
      setPopupInput("");
    } catch (err: any) { alert(err.message || "添加行评论失败"); }
  };

  useEffect(() => {
    if (!popup) return;
    const h = (e: MouseEvent) => { if (!(e.target as HTMLElement).closest(".line-comment-popup") && !(e.target as HTMLElement).closest("[data-line]")) { setPopup(null); contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active")); } };
    document.addEventListener("mousedown", h); return () => document.removeEventListener("mousedown", h);
  }, [popup]);

  useEffect(() => {
    if (!popup) return;
    const h = (e: KeyboardEvent) => { if (e.key === "Escape") { setPopup(null); contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active")); } };
    document.addEventListener("keydown", h); return () => document.removeEventListener("keydown", h);
  }, [popup]);

  return (
    <article className="article-detail">
      <header className="article-header">
        <div className="article-type-row"><span className="article-type-badge">{article.type}</span>
          <div className="article-tags">{article.tags.map((tag) => <Link key={tag} href={`/labels/${tag}`} className="tag-badge">{tag}</Link>)}</div></div>
        <h1 className="article-title">{article.title}</h1>
        <div className="article-meta"><time>发布于 {new Date(article.created_at).toLocaleDateString("zh-CN")}</time>
          {article.created_at !== article.updated_at && <time className="updated-at">· 更新于 {new Date(article.updated_at).toLocaleDateString("zh-CN")}</time>}
          {can_edit && <Link href={`/admin/articles`} className="edit-link">编辑</Link>}</div>
      </header>
      <div className="content-body" ref={contentRef} onClick={handleContentClick} dangerouslySetInnerHTML={{ __html: html ?? "" }} />
      <div className="line-comments-container"><div className="line-comments-panel">
        <div className="line-comments-header"><span>💬 行评论</span><div className="line-comments-header-actions">
          <span className={`line-comments-help ${guideOpen ? "active" : ""}`} onClick={() => setGuideOpen(!guideOpen)} title="使用帮助">?</span>
          <span className="line-comments-toggle" onClick={() => setPanelOpen(!panelOpen)} title={panelOpen ? "隐藏行评论" : "显示行评论"}>{panelOpen ? "×" : "+"}</span></div></div>
        {guideOpen && <div className="line-comments-guide"><div className="line-comments-guide-content"><p><strong>📖 行评论使用说明</strong></p><ul><li>点击行号为该行添加短评</li><li>每条评论最多 <strong>20 字</strong></li><li>面板仅在<strong>宽屏（≥1024px）</strong>显示</li></ul></div></div>}
        {panelOpen && <div className="line-comments-list">{totalLines === 0 ? <div className="line-comments-empty">暂无内容行</div> :
          Array.from({ length: totalLines }, (_, i) => { const ln = i + 1; const count = lineCounts[ln] || 0; const comments = lineDataRef.current[ln] || []; const latest = comments.length > 0 ? (count > 1 ? `💬 ${count}条评论` : comments[comments.length - 1].content) : "";
            return <div key={ln} className={`line-comment-row ${count > 1 ? "has-multi" : ""}`} onClick={(e) => { e.stopPropagation(); const block = contentRef.current?.querySelector(`[data-line="${ln}"]`); if (block) { contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active")); block.classList.add("line-active"); openPopupForLine(ln, block.getBoundingClientRect()); } }}><span className="line-comment-line-num">L{ln}</span><span className="line-comment-text">{latest || "—"}</span>{count > 1 && <span className="line-comment-count">{count}</span>}</div>; })}</div>}
      </div></div>
      {popup && <div className="line-comment-popup" style={{ left: popup.x, top: popup.y }} onClick={(e) => e.stopPropagation()}>
        <div className="line-comment-popup-header"><span className="line-comment-popup-title">第 {popup.lineNum} 行评论</span><button className="line-comment-popup-close" onClick={() => { setPopup(null); contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active")); }}>×</button></div>
        <div className="line-comment-popup-comments">{popupComments.length === 0 ? <div className="line-comment-popup-empty">暂无评论</div> :
          popupComments.map((c, i) => <div key={i} className="line-comment-popup-item"><div className="line-comment-popup-avatar">{(c.author_name || "匿")[0]}</div><div className="line-comment-popup-body"><div className="line-comment-popup-author">{c.author_name} · {new Date(c.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</div><div className="line-comment-popup-content">{c.content}</div></div></div>)}</div>
        {isLoggedIn ? <div className="line-comment-popup-form"><div className="line-comment-input-wrap"><input type="text" className="line-comment-input" maxLength={20} placeholder="输入短评（最多20字）..." value={popupInput} onChange={(e) => setPopupInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") handleLineCommentSubmit(); if (e.key === "Escape") setPopup(null); }} /><span className={`line-comment-counter ${popupInput.length >= 20 ? "overlimit" : ""}`}>{popupInput.length}/20</span></div><button className="line-comment-submit" onClick={handleLineCommentSubmit}>发送</button></div>
        : <div className="line-comment-popup-form"><div className="line-comment-login-hint"><Link href={`/auth/login?next=/${articleType}/${articleSlug}`}>登录</Link>后添加行评论</div></div>}
      </div>}
      {rating && <RatingWidget articleType={articleType} articleSlug={articleSlug} initialAvg={rating.average_score} initialVoters={rating.total_voters} initialUserScore={rating.user_score} />}
      <CommentSection articleType={articleType} articleSlug={articleSlug} initialComments={comments} />
    </article>
  );
}
