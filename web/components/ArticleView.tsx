"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import Link from "next/link";
import type { ArticleDetail, Comment } from "@/lib/types";
import { RatingWidget } from "./RatingWidget";
import { CommentSection } from "./CommentSection";
import { SafeMarkdown } from "./SafeMarkdown";
import { addLineComment, apiUrl } from "@/lib/api";
import { useApp } from "./AppProviders";
import { UserAvatar } from "@/components/admin/UserAvatar";
import { hydrateMarkdown } from "@/lib/markdown-hydrate";
import { LineCommentBubble, CommentAvatar, commentDisplayName, formatBubbleTime } from "./article-view";

interface Props {
  data: ArticleDetail;
  articleType: string;
  articleSlug: string;
}

const youtubeIdRe = /^[A-Za-z0-9_-]{11}$/;

function hydrateYoutubeEmbeds(container: HTMLElement) {
  container.querySelectorAll<HTMLElement>(".wikidot-youtube[data-youtube-id]").forEach((placeholder) => {
    const id = placeholder.dataset.youtubeId || "";
    if (!youtubeIdRe.test(id)) return;
    const iframe = document.createElement("iframe");
    iframe.src = `https://www.youtube.com/embed/${id}`;
    iframe.loading = "lazy";
    iframe.allowFullscreen = true;
    iframe.frameBorder = "0";
    iframe.referrerPolicy = "strict-origin-when-cross-origin";
    iframe.title = "YouTube video";
    iframe.className = "wikidot-yt-embed";
    placeholder.replaceWith(iframe);
  });
}

function isSafeAvatarURL(src: string) {
  if (!src) return false;
  if (src.startsWith("/uploads/") || src.startsWith("/avatars/")) return true;
  try {
    const u = new URL(src, window.location.origin);
    return u.protocol === "https:" || u.protocol === "http:";
  } catch {
    return false;
  }
}

function hydrateUserMentions(container: HTMLElement) {
  container.querySelectorAll<HTMLAnchorElement>("a.user-mention[data-avatar]").forEach((link) => {
    if (link.dataset.userMentionHydrated === "1") return;
    const avatar = (link.dataset.avatar || "").trim();
    if (!isSafeAvatarURL(avatar)) return;
    const img = document.createElement("img");
    img.className = "user-mention-avatar";
    img.src = avatar;
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    link.insertBefore(img, link.firstChild);
    link.dataset.userMentionHydrated = "1";
  });
}

function hydrateObfuscatedEmails(container: HTMLElement) {
  container.querySelectorAll<HTMLAnchorElement>("a.wikidot-email[data-user][data-domain]").forEach((link) => {
    if (link.getAttribute("href")) return;
    const user = (link.dataset.user || "").trim();
    const domain = (link.dataset.domain || "").trim();
    if (!user || !domain || /[\s<>"'`]/.test(user + domain)) return;
    link.href = `mailto:${user}@${domain}`;
  });
}

function hydrateArticleRuntime(container: HTMLElement) {
  hydrateYoutubeEmbeds(container);
  hydrateUserMentions(container);
  hydrateObfuscatedEmails(container);
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
  // Auth (user + csrfToken) comes from the shared <AppProviders> context
  // instead of a per-component getMe/getCsrf fetch — see P1.
  const { user: currentUser, csrfToken } = useApp();
  const isLoggedIn = !!currentUser;
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

  // Measure bubble top + height for each commented line. Used after
  // innerHTML is rendered, on window resize, after data load, and after
  // new comments are submitted.
  //
  // Reads commented-line numbers directly from lineDataRef rather than
  // the commentedLines state so that async callbacks (requestIdleCallback
  // in the content-hydration effect) always see the latest data even if
  // the callback was scheduled before the [data] effect populated the
  // state (classic React stale-closure trap).
  const measureBubbleTops = useCallback((container: HTMLElement) => {
    const lines = Object.keys(lineDataRef.current).map(Number).sort((a, b) => a - b);
    const tops: Record<number, number> = {};
    const heights: Record<number, number> = {};
    lines.forEach((ln) => {
      const el = container.querySelector(`[data-line="${ln}"]`) as HTMLElement | null;
      if (el) {
        tops[ln] = el.offsetTop;
        heights[ln] = el.offsetHeight;
      }
    });
    bubbleMeasureKeyRef.current += 1;
    setBubbleTops(tops);
    setBubbleHeights(heights);
  }, []);

  // Auth is now provided by <AppProviders> (single getMe/getCsrf for the
  // whole page), so this component no longer fires its own auth fetch.

  // Initialise line-comment refs from server data. Runs once per data
  // change (new article navigation). DO NOT include lineCounts/commentedLines
  // in the big hydration effect's dep array — that would cause the whole
  // DOMPurify + KaTeX pass to run twice (once for html, once when this
  // effect sets commentedLines), doubling the time-to-content for long
  // articles like the Typst syntax guide.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLineCounts(rawLineCounts ?? {});
    lineCountsRef.current = rawLineCounts ?? {};
    const allComments = (data as any).line_comments ?? [];
    const d: Record<number, Comment[]> = {};
    allComments.forEach((c: Comment) => { const ln = (c as any).line_number; if (!d[ln]) d[ln] = []; d[ln].push(c); });
    lineDataRef.current = d;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCommentedLines(Object.keys(d).map(Number).sort((a, b) => a - b));
    requestAnimationFrame(() => {
      const container = contentRef.current;
      if (container) measureBubbleTops(container);
    });
  }, [data, measureBubbleTops]);

  // Re-measure on resize (responsive font/wrap can shift line positions).
  useEffect(() => {
    const onResize = () => {
      const container = contentRef.current;
      if (container) measureBubbleTops(container);
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [measureBubbleTops]);

  // Content enhancement effect — runs once per article (html change only).
  //
  // After the server-side PostProcessArticleHTML pass, the HTML arriving
  // in the client is ALREADY fully prepared:
  //   ✓ Sanitised (bluemonday on the server; no scripts/event-handlers)
  //   ✓ YouTube placeholders → real <iframe loading="lazy">
  //   ✓ All <img> have loading="lazy" decoding="async"
  //   ✓ External links have target="_blank" rel=noopener/referrerpolicy
  //   ✓ <video>/<audio> have controls/preload
  //   ✓ Top-level block elements have data-line="N"
  //   ✓ Wrapped in <div class="article-content [typst-content]" data-processed="1">
  //
  // So the client MUST NOT:
  //   • Run DOMPurify (already done server-side; importing it is dead weight)
  //   • Rewrite innerHTML (would force a synchronous layout)
  //   • Add lazy/decode attrs to images (already there)
  //   • Assign data-line (already numbered)
  //
  // What the client still needs to do (things that depend on browser
  // state or runtime data that the server cannot predict):
  //   1. Hydrate browser-side helpers that need preserved data-* attributes
  //      (nested YouTube fallbacks, user mention avatars, obfuscated emails)
  //   2. Wire up Wikidot [[tabview]] click handlers (pure JS event binding)
  //   3. Toggle .has-line-comments class based on live comment counts
  //      (these change over time and are viewer-specific)
  //   4. Deferred: hydrate KaTeX math ($...$) and mermaid diagrams in
  //      Markdown/Wikidot articles (Typst uses native MathML so it
  //      doesn't need KaTeX). These are non-critical for reading and
  //      run via requestIdleCallback so they never block the main thread.
  //   5. Measure comment bubble positions (needs real layout).
  //
  // The content div is pre-populated via dangerouslySetInnerHTML on the
  // first render (SSR + initial client paint), so readers see article
  // text immediately — this effect only performs small, idempotent runtime
  // hydrations and event/class updates after the first paint.
  useEffect(() => {
    const container = contentRef.current;
    if (!container) return;

    let cancelled = false;

    // Detect whether this is a Typst article. Typst compiles math to
    // native MathML, so we skip KaTeX entirely for those — no need to
    // tree-walk thousands of SVG/MathML nodes looking for $...$ markers
    // that don't exist.
    const isTypst = container.querySelector?.(".typst-content") !== null;

    // 1. Restore small runtime behaviours that depend on browser-side data
    //    and preserved data-* attributes.
    hydrateArticleRuntime(container);

    // 2. Wire up Wikidot [[tabview]] clicks (idempotent — harmless if
    //    no tabviews are present).
    const handleTabClick = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null;
      const tab = target?.closest(".wikidot-tab-tab") as HTMLElement | null;
      if (!tab || !container.contains(tab)) return;
      const tv = tab.closest(".wikidot-tabview") as HTMLElement | null;
      if (!tv) return;
      event.preventDefault();
      const id = tab.dataset.tabId;
      if (id === undefined) return;
      const tabs = tv.querySelectorAll<HTMLElement>(".wikidot-tab-tab");
      const panels = tv.querySelectorAll<HTMLElement>(".wikidot-tab-panel");
      tabs.forEach((t) => t.classList.remove("active"));
      panels.forEach((p) => p.classList.toggle("active", p.dataset.tabId === id));
      tab.classList.add("active");
    };
    container.addEventListener("click", handleTabClick);

    // 3. Apply comment-marker dots. data-line is already on elements
    //    from the server; we only toggle the class based on current
    //    comment counts (which change after load).
    const counts = lineCountsRef.current;
    container.querySelectorAll<HTMLElement>("[data-line]").forEach((block) => {
      const n = parseInt(block.getAttribute("data-line")!);
      const count = counts[n] || 0;
      block.classList.toggle("has-line-comments", count > 0);
    });

    // 4. Deferred KaTeX + mermaid hydration. Skipped for Typst (native
    //    MathML, no mermaid blocks). Uses requestIdleCallback so it
    //    never blocks scrolling/interaction.
    if (!isTypst) {
      const schedule = (cb: () => void) => {
        if ("requestIdleCallback" in window) {
          (window as any).requestIdleCallback(() => { if (!cancelled) cb(); }, { timeout: 500 });
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
    } else {
      // For Typst, measure bubble positions on the next frame (layout
      // is settled by then).
      requestAnimationFrame(() => {
        if (!cancelled) measureBubbleTops(container);
      });
    }

    return () => {
      cancelled = true;
      container.removeEventListener("click", handleTabClick);
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
      // Re-measure bubble positions (new line may need positioning)
      requestAnimationFrame(() => {
        const container = contentRef.current;
        if (container) measureBubbleTops(container);
      });
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
                    // apiUrl() 在 dev 模式下服务端返回 http://localhost:8000
                    // (SSR 直接连后端), 客户端返回 "" (走 next proxy)。两端
                    // href 不一致会触发 React hydration 警告。生产环境
                    // NEXT_PUBLIC_API_BASE_URL 配好后两端都是绝对 URL 一致,
                    // 警告自动消失。两种 URL 都能访问同一个 endpoint,
                    // 用 suppressHydrationWarning 静默掉这个已知 mismatch。
                    suppressHydrationWarning
                >📄 下载 PDF</a>
            )}
            {can_edit || isLoggedIn ? <Link href={`/admin/articles?editType=${article.type}&editSlug=${article.slug}`} className="edit-link">✏️ 编辑</Link> : null}</div>
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
              const top = bubbleTops[ln];
              const height = bubbleHeights[ln];
              if (top === undefined || height === undefined) return null;
              return (
                  <LineCommentBubble
                      key={ln}
                      lineNum={ln}
                      comments={[]}
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
          {guideOpen && <div className="line-comments-guide"><div className="line-comments-guide-content"><p><strong>📖 行评论使用说明</strong></p><ul><li>已评论的行左侧显示与行等高的浮泡</li><li>单条：头像+昵称+时间+内容紧凑展示</li><li>多条：折叠显示&quot;点击展开&quot;，点击后展示全部</li><li>点击文章中的任意行可添加新评论</li><li>每条评论最多 <strong>20 字</strong></li></ul></div></div>}
          {panelOpen && <div className="line-comments-list">
            {commentedLines.length === 0
                ? <div className="line-comments-empty">暂无行评论<br /><span className="line-comments-empty-hint">点击文章中的行可添加短评</span></div>
                : commentedLines.map((ln) => {
                  const count = lineCounts[ln] || 0;
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
                      {"—"}
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
