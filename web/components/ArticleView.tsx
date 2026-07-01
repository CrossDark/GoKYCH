"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import Link from "next/link";
import type { ArticleDetail, Comment } from "@/lib/types";
import { RatingWidget } from "./RatingWidget";
import { CommentSection } from "./CommentSection";
import { SafeMarkdown } from "./SafeMarkdown";
import { getMe, getCsrf, addLineComment, apiUrl } from "@/lib/api";
import { UserAvatar } from "@/components/admin/UserAvatar";
import { hydrateMarkdown } from "@/lib/markdown-hydrate";

interface Props {
  data: ArticleDetail;
  articleType: string;
  articleSlug: string;
}

function formatBubbleTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
  } catch { return ""; }
}

// commentDisplayName returns the best display name for a comment:
// nickname (from users table) > author_name (entered at comment time).
function commentDisplayName(c: Comment): string {
  return c.author_nickname && c.author_nickname.trim() !== ""
    ? c.author_nickname
    : (c.author_name || "匿名");
}

// CommentAvatar renders the avatar for a comment using UserAvatar, which
// shows the user's uploaded avatar when available and falls back to a
// gradient circle with the first letter of the display name for anonymous
// comments or users without a custom avatar.
function CommentAvatar({ c, size = 20 }: { c: Comment; size?: number }) {
  return (
    <UserAvatar
      user={{
        nickname: c.author_nickname || "",
        username: c.author_name || "匿名",
        avatar: c.author_avatar || "",
      }}
      size={size}
    />
  );
}

function LineCommentBubble({
  lineNum,
  comments,
  top,
  height,
  onClickLine,
}: {
  lineNum: number;
  comments: Comment[];
  top: number;
  height: number;
  onClickLine: (ln: number) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const sorted = [...comments].sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
  );

  // Multiple comments: collapsed by default, click to expand to show all.
  if (sorted.length > 1) {
    if (!expanded) {
      return (
        <div
          className="line-bubble line-bubble-collapsed"
          style={{ top, height }}
          onClick={(e) => e.stopPropagation()}
        >
          <button
            className="line-bubble-expand-btn"
            onClick={(e) => { e.stopPropagation(); setExpanded(true); }}
            title={`第 ${lineNum} 行 · ${sorted.length} 条评论`}
          >
            <span className="line-bubble-expand-icon">▸</span>
            <span>第 {lineNum} 行 · {sorted.length} 条评论 · 点击展开</span>
          </button>
        </div>
      );
    }

    // Expanded: full list, auto-height (taller than line)
    return (
      <div
        className="line-bubble line-bubble-expanded"
        style={{ top }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="line-bubble-expanded-header">
          <span>第 {lineNum} 行 · {sorted.length} 条评论</span>
          <button
            className="line-bubble-collapse-btn"
            onClick={(e) => { e.stopPropagation(); setExpanded(false); }}
            title="收起"
          >▾</button>
        </div>
        <div className="line-bubble-comment-list">
          {sorted.map((c) => (
            <div key={c.id} className="line-bubble-comment-item">
              <CommentAvatar c={c} size={20} />
              <span className="line-bubble-name">{commentDisplayName(c)}</span>
              <span className="line-bubble-time">· {formatBubbleTime(c.created_at)}</span>
      <span className="line-bubble-content">
        <SafeMarkdown html={c.content_html} text={c.content} />
      </span>
            </div>
          ))}
        </div>
        <button
          className="line-bubble-line-link"
          onClick={(e) => { e.stopPropagation(); onClickLine(lineNum); }}
        >定位到第 {lineNum} 行 →</button>
      </div>
    );
  }

  // Single comment: one-line compact display
  const c = sorted[0];
  return (
    <div
      className="line-bubble line-bubble-single"
      style={{ top, height, "--bubble-h": `${height}px` } as React.CSSProperties}
      onClick={(e) => e.stopPropagation()}
    >
      <CommentAvatar c={c} size={Math.min(height - 4, 20)} />
      <span className="line-bubble-name">{commentDisplayName(c)}</span>
      <span className="line-bubble-time">· {formatBubbleTime(c.created_at)}</span>
      <span className="line-bubble-content">{c.content}</span>
      <button
        className="line-bubble-line-link"
        onClick={(e) => { e.stopPropagation(); onClickLine(lineNum); }}
        title={`定位到第 ${lineNum} 行`}
      >→</button>
    </div>
  );
}

export function ArticleView({ data, articleType, articleSlug }: Props) {
  const { article, html, rating, comments: rawComments, line_comment_counts: rawLineCounts, can_edit } = data;
  const comments = rawComments ?? [];
  // Backend omits `tags` when the article has none (omitempty); coerce to []
  // so the header tag list doesn't blow up with "Cannot read .map of undefined".
  const articleTags = (article as any).tags ?? [];
  const contentRef = useRef<HTMLDivElement>(null);
  const lineCountsRef = useRef<Record<number, number>>({});
  const lineDataRef = useRef<Record<number, Comment[]>>({});

  const [lineCounts, setLineCounts] = useState<Record<number, number>>(rawLineCounts ?? {});
  const [isLoggedIn, setIsLoggedIn] = useState(false);
  const [currentUser, setCurrentUser] = useState<{ nickname?: string; username: string } | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [panelOpen, setPanelOpen] = useState(true);
  const [guideOpen, setGuideOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const [popup, setPopup] = useState<{ lineNum: number; x: number; y: number } | null>(null);
  const [popupInput, setPopupInput] = useState("");
  const [popupComments, setPopupComments] = useState<Comment[]>([]);

  // Commented lines (for panel + bubble layer)
  const [commentedLines, setCommentedLines] = useState<number[]>([]);

  // Bubble vertical positions (line number → top in px relative to content-body)
  const [bubbleTops, setBubbleTops] = useState<Record<number, number>>({});
  // Bubble heights — match the commented line's offsetHeight so the bubble
  // visually aligns with the line and auto-shrinks font to fit.
  const [bubbleHeights, setBubbleHeights] = useState<Record<number, number>>({});
  const bubbleMeasureKeyRef = useRef(0);

  // Auth
  useEffect(() => {
    getMe().then((r) => {
      if (r.user) { setIsLoggedIn(true); setCurrentUser(r.user); getCsrf().then((c) => setCsrfToken(c.csrf_token)).catch(() => {}); }
    }).catch(() => {});
  }, []);

  // Initialise line-comment refs from server data. Runs once per data
  // change (new article navigation). DO NOT include lineCounts/commentedLines
  // in the big hydration effect's dep array — that would cause the whole
  // DOMPurify + KaTeX pass to run twice (once for html, once when this
  // effect sets commentedLines), doubling the time-to-content for long
  // articles like the Typst syntax guide.
  useEffect(() => {
    setLineCounts(rawLineCounts ?? {});
    lineCountsRef.current = rawLineCounts ?? {};
    const allComments = (data as any).line_comments ?? [];
    const d: Record<number, Comment[]> = {};
    allComments.forEach((c: Comment) => { const ln = (c as any).line_number; if (!d[ln]) d[ln] = []; d[ln].push(c); });
    lineDataRef.current = d;
    setCommentedLines(Object.keys(d).map(Number).sort((a, b) => a - b));
  }, [data]);

  // Measure bubble top + height for each commented line. Used both after
  // innerHTML is rendered and on window resize.
  const measureBubbleTops = useCallback((container: HTMLElement) => {
    const tops: Record<number, number> = {};
    const heights: Record<number, number> = {};
    commentedLines.forEach((ln) => {
      const el = container.querySelector(`[data-line="${ln}"]`) as HTMLElement | null;
      if (el) {
        tops[ln] = el.offsetTop;
        heights[ln] = el.offsetHeight;
      }
    });
    bubbleMeasureKeyRef.current += 1;
    setBubbleTops(tops);
    setBubbleHeights(heights);
  }, [commentedLines]);

  // Re-measure on resize (responsive font/wrap can shift line positions).
  useEffect(() => {
    const onResize = () => {
      const container = contentRef.current;
      if (container) measureBubbleTops(container);
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [measureBubbleTops]);

  // Content hydration effect — runs once per article (html change only).
  //
  // CRITICAL PERFORMANCE NOTE:
  // The content div is pre-populated via dangerouslySetInnerHTML on the
  // FIRST render (both SSR and the initial client paint), so readers see
  // article text immediately without waiting for JS to download, parse,
  // and hydrate. This effect then:
  //   1. Runs DOMPurify as defense-in-depth (backend already sanitises).
  //   2. Rewrites YouTube placeholders → lazy <iframe>.
  //   3. Wires up tabview click handlers.
  //   4. Assigns data-line attributes for line-comment positioning.
  //   5. Hydrates KaTeX math (async, yields to browser between blocks).
  //   6. Hydrates mermaid diagrams (async, yields to browser).
  //   7. Measures bubble positions.
  //
  // Deps are ONLY [html] — adding lineCounts/commentedLines here causes
  // the ENTIRE heavy pass (DOMPurify + KaTeX over every text node) to
  // run TWICE for long articles, which is the main reason Typst pages
  // like the syntax guide appeared "extremely slow" — the user saw the
  // server-rendered content immediately, but then React wiped it and
  // re-processed the whole DOM a second time, blocking the main thread.
  useEffect(() => {
    const container = contentRef.current;
    if (!container) return;

    let cancelled = false;
    (async () => {
      const DOMPurify = (await import("dompurify")).default;
      if (cancelled) return;
      const purifyCfg = {
        ALLOWED_TAGS: [
          "a", "abbr", "aside", "b", "blockquote", "br", "cite", "code",
          "details", "div", "dl", "dt", "dd", "em", "figcaption", "figure",
          "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "ins",
          "kbd", "li", "mark", "ol", "p", "pre", "s", "section", "small",
          "span", "strong", "sub", "summary", "sup", "table", "tbody",
          "td", "th", "thead", "tr", "u", "ul",
          // Typst HTML output uses inline SVG extensively for
          // math glyphs, shapes, and decorated boxes. Without
          // these, DOMPurify strips all SVG content and Typst
          // pages render as broken/missing text.
          "svg", "path", "g", "text", "rect", "circle", "ellipse", "line",
          "polyline", "polygon", "defs", "clippath", "marker", "mask",
          "pattern", "lineargradient", "radialgradient", "stop", "use",
          "image", "foreignObject",
        ],
        ALLOWED_ATTR: [
          "href", "title", "alt", "src", "class", "style", "id", "target",
          "rel", "colspan", "rowspan", "data-line", "data-tab-id",
          "data-toggle", "data-source",
          // SVG attributes required by Typst output
          "d", "fill", "stroke", "stroke-width", "viewBox", "width", "height",
          "x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry",
          "transform", "font-family", "font-size", "font-weight", "font-style",
          "text-anchor", "dominant-baseline", "points",
          "clip-path", "mask", "fill-opacity", "stroke-opacity", "stroke-linecap",
          "stroke-linejoin", "stroke-dasharray", "offset", "stop-color", "stop-opacity",
          "xmlns", "xmlns:xlink", "xlink:href", "data-math-rendered", "data-math-error",
          "data-mermaid-rendered", "data-mermaid-error",
        ],
        ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|javascript:;$|[#/])/i,
        FORBID_TAGS: ["script", "object", "embed", "form"],
        FORBID_ATTR: ["onerror", "onload", "onclick", "onmouseover", "onfocus", "onblur"],
        ADD_TAGS: ["iframe"],
        ADD_ATTR: ["allowfullscreen", "frameborder", "referrerpolicy", "loading"],
      };
      const safe = DOMPurify.sanitize(html ?? "", purifyCfg);
      // Only overwrite innerHTML if the sanitised output differs from
      // what's already there (which is the server-rendered HTML). On the
      // first client paint the div already contains the raw server HTML;
      // if DOMPurify didn't strip anything, we skip the write to avoid
      // a forced synchronous layout.
      if (container.innerHTML !== safe) {
        container.innerHTML = safe;
      }

      // Wikidot `[[youtube ID]]` emits a placeholder div; swap for iframe
      // after sanitisation (we re-allowed iframe + its attrs above).
      container.querySelectorAll<HTMLElement>(".wikidot-youtube[data-youtube-id]").forEach((el) => {
        const id = el.dataset.youtubeId;
        if (!id) return;
        const iframe = document.createElement("iframe");
        iframe.src = `https://www.youtube.com/embed/${id}`;
        iframe.loading = "lazy";
        iframe.allowFullscreen = true;
        iframe.setAttribute("frameborder", "0");
        iframe.setAttribute("referrerpolicy", "strict-origin-when-cross-origin");
        iframe.setAttribute("title", "YouTube video");
        el.replaceChildren(iframe);
      });

      // Wikidot [[tabview]] — wire up click-to-switch behaviour.
      container.querySelectorAll<HTMLElement>(".wikidot-tabview").forEach((tv) => {
        const tabs = tv.querySelectorAll<HTMLElement>(".wikidot-tab-tab");
        const panels = tv.querySelectorAll<HTMLElement>(".wikidot-tab-panel");
        tabs.forEach((tab) => {
          tab.addEventListener("click", (e) => {
            e.preventDefault();
            const id = tab.dataset.tabId;
            if (id === undefined) return;
            tabs.forEach((t) => t.classList.remove("active"));
            panels.forEach((p) => p.classList.remove("active"));
            tab.classList.add("active");
            const target = tv.querySelector<HTMLElement>(
              `.wikidot-tab-panel[data-tab-id="${id}"]`,
            );
            if (target) target.classList.add("active");
          });
        });
      });

      // Assign line numbers to block elements. Skip nested elements
      // inside pre/li/table, auto-generated TOC, tab nav, etc.
      const blocks = container.querySelectorAll("p, h1, h2, h3, h4, h5, h6, li, pre, blockquote, div, table");
      let ln = 0;
      blocks.forEach((block) => {
        const el = block as HTMLElement;
        if (el.closest("pre") && el.tagName !== "PRE") return;
        if (el.tagName !== "LI" && el.closest("li") && el.closest("li") !== el) return;
        if (el.tagName !== "TABLE" && el.closest("table")) return;
        const tocRoot = el.closest(".wikidot-toc");
        if (tocRoot && tocRoot !== el) return;
        if (el.tagName === "LI" && el.closest(".wikidot-tab-nav")) return;
        if (el.classList.contains("wikidot-tab-panels") || el.classList.contains("wikidot-tab-panel")) return;
        if (el.classList.contains("collapsible-content")) return;
        if (el.classList.contains("wikidot-align")) return;
        if (el.closest("section.footnotes") && el.tagName !== "SECTION") return;
        ln++;
        el.setAttribute("data-line", String(ln));
      });

      // Apply comment marker dots.
      const counts = lineCountsRef.current;
      container.querySelectorAll("[data-line]").forEach((block) => {
        const n = parseInt(block.getAttribute("data-line")!);
        const count = counts[n] || 0;
        block.classList.toggle("has-line-comments", count > 0);
      });

      // Hydrate KaTeX + mermaid. Yield to the browser between sync/async
      // phases so the user can scroll/interact while math renders in.
      // requestIdleCallback (fallback to setTimeout) ensures long Typst
      // articles don't block the main thread for hundreds of ms.
      const schedule = (cb: () => void) => {
        if ("requestIdleCallback" in window) {
          (window as any).requestIdleCallback(() => { if (!cancelled) cb(); }, { timeout: 200 });
        } else {
          setTimeout(() => { if (!cancelled) cb(); }, 0);
        }
      };
      schedule(() => {
        if (cancelled || !container) return;
        hydrateMarkdown(container).then(() => {
          if (!cancelled) measureBubbleTops(container);
        });
      });
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [html]);

  const scrollToLine = useCallback((ln: number) => {
    const container = contentRef.current;
    if (!container) return;
    const block = container.querySelector(`[data-line="${ln}"]`) as HTMLElement | null;
    if (block) {
      container.querySelectorAll(".line-active").forEach((el) => el.classList.remove("line-active"));
      block.classList.add("line-active");
      block.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }, []);

  const openPopupForLine = useCallback((ln: number, rect: DOMRect) => {
    const cmts = lineDataRef.current[ln] || [];
    setPopupComments(cmts);
    setPopupInput("");
    // Position popup to the right of the content, clamped to viewport
    const x = Math.min(rect.right + 12, window.innerWidth - 290);
    const y = Math.min(Math.max(rect.top - 20, 10), window.innerHeight - 360);
    setPopup({ lineNum: ln, x, y });
  }, []);

  const closePopup = useCallback(() => {
    setPopup(null);
    contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active"));
  }, []);

  // Narrow viewport: line-comment UI is hidden via CSS (line-bubble-layer is
  // display:none below 1024px, and the side panel never appears). The click
  // handler also has to bail out, otherwise tapping a paragraph on a phone
  // would still pop a "add line comment" dialog that the user has no way to
  // see the comments from.
  const isNarrowViewport = () => typeof window !== "undefined" && window.innerWidth < 1024;

  const handleContentClick = (e: React.MouseEvent) => {
    if (isNarrowViewport()) return;
    const target = (e.target as HTMLElement).closest("[data-line]") as HTMLElement;
    if (!target) return;
    const ln = parseInt(target.getAttribute("data-line")!);
    if (!ln) return;
    contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active"));
    target.classList.add("line-active");
    openPopupForLine(ln, target.getBoundingClientRect());
  };

  const handleLineCommentSubmit = async () => {
    if (!popupInput.trim() || !popup || !csrfToken || submitting) return;
    setSubmitting(true);
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
      // Update commented lines list
      setCommentedLines(Object.keys(lineDataRef.current).map(Number).sort((a, b) => a - b));
      // Re-apply marker on the block
      const block = contentRef.current?.querySelector(`[data-line="${popup.lineNum}"]`);
      if (block) block.classList.add("has-line-comments");
    } catch (err: any) { alert(err.message || "添加行评论失败"); }
    finally { setSubmitting(false); }
  };

  // Close popup on outside click
  useEffect(() => {
    if (!popup) return;
    const h = (e: MouseEvent) => { if (!(e.target as HTMLElement).closest(".line-comment-popup") && !(e.target as HTMLElement).closest("[data-line]")) { closePopup(); } };
    document.addEventListener("mousedown", h); return () => document.removeEventListener("mousedown", h);
  }, [popup, closePopup]);

  // Close popup on Escape
  useEffect(() => {
    if (!popup) return;
    const h = (e: KeyboardEvent) => { if (e.key === "Escape") { closePopup(); } };
    document.addEventListener("keydown", h); return () => document.removeEventListener("keydown", h);
  }, [popup, closePopup]);

  return (
    <article className="article-detail">
      <header className="article-header">
        <div className="article-type-row"><span className="article-type-badge">{article.type}</span>
          <div className="article-tags">{articleTags.map((tag: string) => <Link key={tag} href={`/labels/${tag}`} className="tag-badge">{tag}</Link>)}</div></div>
        <h1 className="article-title">{article.title}</h1>
        <div className="article-meta">
          {article.author_name && (
            <span className="article-author">
              <UserAvatar
                user={{
                  nickname: article.author_nickname || "",
                  username: article.author_name || "",
                  avatar: article.author_avatar || "",
                }}
                size={28}
              />
              <span>{article.author_nickname || article.author_name}</span>
            </span>
          )}
          <time>发布于 {new Date(article.created_at).toLocaleDateString("zh-CN")}</time>
          {article.created_at !== article.updated_at && <time className="updated-at">· 更新于 {new Date(article.updated_at).toLocaleDateString("zh-CN")}</time>}
          {article.type === "typst" && (
            <a
              href={apiUrl(`/api/articles/${article.type}/${article.slug}/pdf`)}
              className="edit-link"
              download
              title="下载 PDF（首次点击会触发 typst 编译，约 1–2 秒）"
            >📄 下载 PDF</a>
          )}
          {can_edit && <Link href={`/admin/articles?editType=${article.type}&editSlug=${article.slug}`} className="edit-link">✏️ 编辑</Link>}</div>
      </header>
      <div className="article-content-wrap">
        <div
          className="content-body"
          ref={contentRef}
          onClick={handleContentClick}
          // Pre-populate with server-rendered HTML so content is visible
          // in the SSR response AND on the first client paint — no blank
          // wait for JS hydration. The useEffect above re-runs DOMPurify
          // as defense-in-depth and only overwrites innerHTML if the
          // sanitised output differs, so we avoid a redundant DOM write
          // for the common case where server output is already clean.
          dangerouslySetInnerHTML={{ __html: html ?? "" }}
          suppressHydrationWarning
        />
        {/* Line comment bubbles — outside the text area, aligned with each commented line */}
        <div className="line-bubble-layer" aria-hidden={false}>
          {commentedLines.map((ln) => {
            const cmts = lineDataRef.current[ln] || [];
            if (cmts.length === 0) return null;
            const top = bubbleTops[ln];
            const height = bubbleHeights[ln];
            if (top === undefined || height === undefined) return null;
            return (
              <LineCommentBubble
                key={ln}
                lineNum={ln}
                comments={cmts}
                top={top}
                height={height}
                onClickLine={(n) => {
                  scrollToLine(n);
                  const block = contentRef.current?.querySelector(`[data-line="${n}"]`);
                  if (block) openPopupForLine(n, block.getBoundingClientRect());
                }}
              />
            );
          })}
        </div>
      </div>

      {/* Side panel */}
      <div className="line-comments-container"><div className="line-comments-panel">
        <div className="line-comments-header"><span>行评论</span><div className="line-comments-header-actions">
          <span className={`line-comments-help ${guideOpen ? "active" : ""}`} onClick={() => setGuideOpen(!guideOpen)} title="使用帮助">?</span>
          <span className="line-comments-toggle" onClick={() => setPanelOpen(!panelOpen)} title={panelOpen ? "隐藏行评论" : "显示行评论"}>{panelOpen ? "×" : "+"}</span></div></div>
        {guideOpen && <div className="line-comments-guide"><div className="line-comments-guide-content"><p><strong>📖 行评论使用说明</strong></p><ul><li>已评论的行左侧显示与行等高的浮泡</li><li>单条：头像+昵称+时间+内容紧凑展示</li><li>多条：折叠显示"点击展开"，点击后展示全部</li><li>点击文章中的任意行可添加新评论</li><li>每条评论最多 <strong>20 字</strong></li></ul></div></div>}
        {panelOpen && <div className="line-comments-list">
          {commentedLines.length === 0
            ? <div className="line-comments-empty">暂无行评论<br /><span className="line-comments-empty-hint">点击文章中的行可添加短评</span></div>
            : commentedLines.map((ln) => {
                const count = lineCounts[ln] || 0;
                const cmts = lineDataRef.current[ln] || [];
                const latest = cmts.length > 0 ? cmts[cmts.length - 1].content : "";
                return (
                  <div key={ln} className="line-comment-row" onClick={(e) => {
                    e.stopPropagation();
                    const block = contentRef.current?.querySelector(`[data-line="${ln}"]`);
                    if (block) {
                      contentRef.current?.querySelectorAll(".line-active").forEach(el => el.classList.remove("line-active"));
                      block.classList.add("line-active");
                      openPopupForLine(ln, block.getBoundingClientRect());
                      block.scrollIntoView({ behavior: "smooth", block: "center" });
                    }
                  }}>
                    <span className="line-comment-line-num">L{ln}</span>
                    <span className="line-comment-text">
                      {latest
                        ? <SafeMarkdown html={(cmts[cmts.length - 1] as any).content_html} text={latest} />
                        : "—"}
                    </span>
                    {count > 1 && <span className="line-comment-count">{count}</span>}
                  </div>
                );
              })
          }
        </div>}
      </div></div>

      {/* Popup */}
      {popup && <div className="line-comment-popup" style={{ left: popup.x, top: popup.y }} onClick={(e) => e.stopPropagation()}>
        <div className="line-comment-popup-header">
          <span className="line-comment-popup-title">第 {popup.lineNum} 行</span>
          <span className="line-comment-popup-count">{popupComments.length} 条评论</span>
          <button className="line-comment-popup-close" onClick={closePopup}>×</button>
        </div>
        <div className="line-comment-popup-comments">{popupComments.length === 0 ? <div className="line-comment-popup-empty">暂无评论，来说两句吧</div> :
          popupComments.map((c) => <div key={c.id} className="line-comment-popup-item"><CommentAvatar c={c} size={28} /><div className="line-comment-popup-body"><div className="line-comment-popup-author">{commentDisplayName(c)} · {new Date(c.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</div><div className="line-comment-popup-content"><SafeMarkdown html={c.content_html} text={c.content} /></div></div></div>)}</div>
        {isLoggedIn ? <div className="line-comment-popup-form"><div className="line-comment-input-wrap"><input type="text" className="line-comment-input" maxLength={20} placeholder="输入短评（最多20字）..." value={popupInput} onChange={(e) => setPopupInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter" && !submitting) handleLineCommentSubmit(); if (e.key === "Escape") closePopup(); }} disabled={submitting} /><span className={`line-comment-counter ${popupInput.length >= 20 ? "overlimit" : ""}`}>{popupInput.length}/20</span></div><button className="line-comment-submit" onClick={handleLineCommentSubmit} disabled={submitting || !popupInput.trim()}>{submitting ? "…" : "发送"}</button></div>
        : <div className="line-comment-popup-form"><div className="line-comment-login-hint"><Link href={`/auth/login?next=/${articleType}/${articleSlug}`}>登录</Link>后添加行评论</div></div>}
      </div>}
      {rating && <RatingWidget articleType={articleType} articleSlug={articleSlug} initialAvg={rating.average_score} initialVoters={rating.total_voters} initialUserScore={rating.user_score} />}
      <CommentSection articleType={articleType} articleSlug={articleSlug} initialComments={comments} />
    </article>
  );
}
