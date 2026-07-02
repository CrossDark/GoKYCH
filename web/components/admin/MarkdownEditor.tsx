"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  hydrateMarkdown,
  preprocessMathBlocks,
  postprocessMathBlocks,
} from "@/lib/markdown-hydrate";
import { renderBBCode } from "@/lib/bbcode-render";
import { renderWikidot } from "@/lib/wikidot-render";

// MarkdownEditor — textarea + client-side preview
// for the article admin editor.
//
// Modes:
//   - "edit"    : only the textarea is visible
//   - "preview" : only the rendered HTML is visible
//                 (renders the markdown source through
//                 a client-side `marked` pass and
//                 sanitises via DOMPurify, so the
//                 author sees the same output the
//                 article view will produce — but
//                 without sending the source to the
//                 backend on every keystroke)
//   - "split"   : side-by-side on wide viewports,
//                 stacks on narrow viewports.
//                 Edit-pane and preview-pane scroll
//                 in lockstep (pixel-ratio sync).
//
// Post-processing:
//   After `marked` + DOMPurify the preview HTML is
//   walked and `$$…$$`, `\[…\]`, `\(…\)`, `$…$`
//   patterns in text nodes are replaced with KaTeX
//   output, and ` ```mermaid ` code blocks are
//   rendered as SVG. Both happen via
//   hydrateMarkdown() — the same helper used by the
//   public article view, so author preview matches
//   public output 1:1.
//
// Server-rendering is skipped on purpose: this
// component is admin-only and only runs after the
// user has clicked into the editor form, so the
// SSR-skip cost is negligible.
//
// For non-md types:
//   - bbcode/wikidot: client-side renderers that mirror the Go backend
//   - html: sanitised via DOMPurify and shown directly
//   - typst: requires the typst CLI to compile (PDF + HTML), so preview
//            is not supported client-side — the author can verify after save.

type Mode = "edit" | "preview" | "split";

interface Props {
  /** Current markdown source. */
  value: string;
  /** Called on every keystroke. */
  onChange: (next: string) => void;
  /** Article type — only "md" gets a live preview. */
  type: string;
  /** Optional id for the underlying textarea
   *  (form `htmlFor` binding on the parent label). */
  id?: string;
  /** Placeholder when empty. */
  placeholder?: string;
  /** Textarea row count. */
  rows?: number;
  /** Whether the textarea is required (form
   *  validation). */
  required?: boolean;
  /** Optional className for the wrapper (CSS hook
   *  for layout). */
  className?: string;
}

export function MarkdownEditor({
  value,
  onChange,
  type,
  id,
  placeholder = "支持 Markdown 语法 · 行内公式 $x^2$ · 块公式 $$E=mc^2$$ · ```mermaid``` 图表",
  rows = 16,
  required = false,
  className,
}: Props) {
  // md, bbcode, wikidot, html all support client-side preview.
  // typst requires the native CLI binary — no browser preview.
  const supportsPreview = type === "md" || type === "bbcode" || type === "wikidot" || type === "html";
  const [mode, setMode] = useState<Mode>("split");
  // Rendered HTML, populated by an async effect.
  // Lives in state (not the DOM) so React reconciles
  // the `<div>` like any other content.
  const [rendered, setRendered] = useState<string>("");
  const [rendering, setRendering] = useState(false);

  // Refs for sync-scroll + hydration targets. We use callback refs for
// the preview pane because it's rendered conditionally (only when the
// source has content AND hydration has finished) — a plain ref
// captured during the first effect run might still be null. Storing
// the element in state lets the sync-scroll effect re-run once the
// preview div actually mounts.
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [previewNode, setPreviewNode] = useState<HTMLDivElement | null>(null);
  const previewCallbackRef = useCallback((node: HTMLDivElement | null) => {
    setPreviewNode(node);
  }, []);
  // Track the source the hydration has been applied to so we
  // don't run hydrateMarkdown on every preview rerender — only
  // when the rendered HTML actually changes.
  const hydratedKeyRef = useRef<string>("");

  // Render the source to HTML. Re-runs whenever the source or type
  // changes (debounced so a fast typist doesn't queue a parser pass
  // per keystroke). Routes to the appropriate renderer:
  //   - md: marked + DOMPurify + KaTeX/Mermaid hydration (existing path)
  //   - bbcode: client-side BBCode renderer (mirrors Go backend)
  //   - wikidot: client-side Wikidot renderer (core syntax; server
  //             features like include/module show placeholders)
  //   - html: DOMPurify sanitisation only — author-supplied HTML is
  //           treated as already-formed markup
  //   - typst: not supported client-side (requires native CLI)
  useEffect(() => {
    if (!supportsPreview || (mode === "edit" && value === "")) {
      return;
    }
    if (mode === "edit") {
      return;
    }
    let cancelled = false;
    setRendering(true);
    const handle = setTimeout(async () => {
      try {
        const DOMPurify = (await import("dompurify")).default;
        if (cancelled) return;

        let html = "";

        if (type === "md") {
          const { marked } = await import("marked");
          marked.setOptions({
            gfm: true,
            breaks: true,
          });
          const { source: preSource, blocks } = preprocessMathBlocks(value || "");
          const rawHtml = marked.parse(preSource) as string;
          const safe = DOMPurify.sanitize(rawHtml, {
            ALLOWED_TAGS: [
              "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
              "h4", "h5", "h6", "hr", "i", "img", "ins", "kbd", "li", "mark",
              "ol", "p", "pre", "s", "small", "span", "strong", "sub", "sup",
              "table", "tbody", "td", "th", "thead", "tr", "u", "ul", "del",
              "figure", "figcaption", "details", "summary", "input",
              "div", "cite",
            ],
            ALLOWED_ATTR: [
              "href", "title", "alt", "src", "class", "target", "rel",
              "checked", "type", "disabled", "style",
              "data-math-rendered",
            ],
            ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
          });
          html = await postprocessMathBlocks(safe, blocks);
        } else if (type === "bbcode") {
          const rawHtml = renderBBCode(value || "");
          html = DOMPurify.sanitize(rawHtml, {
            ALLOWED_TAGS: [
              "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
              "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre",
              "s", "span", "strong", "sub", "sup", "table", "tbody", "td",
              "th", "thead", "tr", "u", "ul", "del", "details", "summary",
              "div", "cite", "video", "audio", "source",
            ],
            ALLOWED_ATTR: [
              "href", "title", "alt", "src", "class", "target", "rel",
              "style", "controls", "loading",
            ],
            ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
          });
        } else if (type === "wikidot") {
          const rawHtml = renderWikidot(value || "");
          html = DOMPurify.sanitize(rawHtml, {
            ALLOWED_TAGS: [
              "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
              "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre",
              "s", "span", "strong", "sub", "sup", "table", "tbody", "td",
              "th", "thead", "tr", "u", "ul", "del", "details", "summary",
              "div", "cite",
            ],
            ALLOWED_ATTR: [
              "href", "title", "alt", "src", "class", "target", "rel",
              "style", "loading",
            ],
            ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
          });
        } else if (type === "html") {
          // User-authored HTML: sanitize but allow a reasonable tag set.
          // DOMPurify's default is already restrictive; we add a few
          // structural tags that the article renderer supports.
          html = DOMPurify.sanitize(value || "", {
            ALLOWED_TAGS: [
              "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
              "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre",
              "s", "small", "span", "strong", "sub", "sup", "table",
              "tbody", "td", "th", "thead", "tr", "u", "ul", "del",
              "figure", "figcaption", "details", "summary", "div",
              "article", "section", "header", "footer", "aside", "nav",
            ],
            ALLOWED_ATTR: [
              "href", "title", "alt", "src", "class", "target", "rel",
              "style", "loading",
            ],
            ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
          });
        }

        if (!cancelled) setRendered(html);
      } finally {
        if (!cancelled) setRendering(false);
      }
    }, 120);
    return () => {
      cancelled = true;
      clearTimeout(handle);
    };
  }, [value, mode, supportsPreview, type]);

  // After React commits the `rendered` HTML into the
  // preview pane, run hydrateMarkdown() to swap raw
  // `$...$` / `$$...$$` / ` ```math ` text into KaTeX
  // and ` ```mermaid ` blocks into SVG. Only applies
  // to markdown output (other renderers handle their
  // own math/mermaid or don't support it). We key on
  // a hash of the HTML so re-renders triggered by the
  // sync-scroll effect don't re-hydrate needlessly.
  useEffect(() => {
    if (mode === "edit") return;
    if (type !== "md") return; // KaTeX/Mermaid only for markdown
    const root = previewNode;
    if (!root || rendered === "") return;
    const key = `${rendered.length}|${rendered.slice(0, 64)}|${rendered.slice(-64)}`;
    if (hydratedKeyRef.current === key) return;
    hydratedKeyRef.current = key;
    let cancelled = false;
    (async () => {
      await hydrateMarkdown(root);
      if (cancelled) return;
    })();
    return () => {
      cancelled = true;
    };
  }, [rendered, mode, previewNode, type]);

  // Sync-scroll in split mode: when the user scrolls
  // either pane, propagate the proportional scroll
  // position to the other.
  //
  // Anti-feedback-loop strategy: instead of a brittle
  // timing-based flag (requestAnimationFrame can fire
  // before the programmatic scroll event is delivered,
  // causing oscillations that make the page jump), we
  // track the exact scrollTop value we just set on the
  // peer element. When that peer fires a scroll event
  // with that exact value (within 1px epsilon), we
  // ignore it as our own echo. This is immune to
  // event-dispatch timing differences across browsers.
  //
  // The effect re-runs whenever `previewNode` changes
  // because the preview div mounts asynchronously
  // (after the source has content + hydration runs);
  // a plain ref captured on first render would still
  // be null at that point.
  useEffect(() => {
    if (mode !== "split") return;
    const textarea = textareaRef.current;
    const preview = previewNode;
    if (!textarea || !preview) return;

    // Last programmatic scrollTop set on each element.
    // When a scroll event's scrollTop matches this value
    // (within EPSILON), it's our own echo — skip it.
    const EPSILON = 1;
    let lastSetTextarea = -Infinity;
    let lastSetPreview = -Infinity;

    const onTextareaScroll = () => {
      const top = textarea.scrollTop;
      // Ignore echoes from programmatic sync
      if (Math.abs(top - lastSetTextarea) <= EPSILON) return;
      const denom = textarea.scrollHeight - textarea.clientHeight;
      if (denom <= 0) return;
      const ratio = top / denom;
      const pDenom = preview.scrollHeight - preview.clientHeight;
      const target = ratio * Math.max(0, pDenom);
      lastSetPreview = target;
      preview.scrollTop = target;
    };
    const onPreviewScroll = () => {
      const top = preview.scrollTop;
      if (Math.abs(top - lastSetPreview) <= EPSILON) return;
      const denom = preview.scrollHeight - preview.clientHeight;
      if (denom <= 0) return;
      const ratio = top / denom;
      const tDenom = textarea.scrollHeight - textarea.clientHeight;
      const target = ratio * Math.max(0, tDenom);
      lastSetTextarea = target;
      textarea.scrollTop = target;
    };
    textarea.addEventListener("scroll", onTextareaScroll, { passive: true });
    preview.addEventListener("scroll", onPreviewScroll, { passive: true });
    return () => {
      textarea.removeEventListener("scroll", onTextareaScroll);
      preview.removeEventListener("scroll", onPreviewScroll);
    };
  }, [mode, previewNode]);

  // Memo the wrapper class so React doesn't re-render
  // when only `value` changes.
  const wrapperClass = useMemo(
    () => `markdown-editor ${className ?? ""}`.trim(),
    [className],
  );

  return (
    <div className={wrapperClass} data-mode={mode}>
      {/* Mode toggle bar */}
      <div className="markdown-editor-toolbar" role="tablist" aria-label="编辑器视图">
        <button
          type="button"
          role="tab"
          aria-selected={mode === "edit"}
          aria-controls={`${id ?? "md-editor"}-edit-pane`}
          className={`markdown-editor-tab ${mode === "edit" ? "active" : ""}`}
          onClick={() => setMode("edit")}
        >
          ✏️ 编辑
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "preview"}
          aria-controls={`${id ?? "md-editor"}-preview-pane`}
          className={`markdown-editor-tab ${mode === "preview" ? "active" : ""}`}
          onClick={() => setMode("preview")}
          disabled={!supportsPreview}
          title={supportsPreview ? "预览渲染结果" : "Typst 类型不支持客户端预览"}
        >
          👁 预览
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "split"}
          aria-controls={`${id ?? "md-editor"}-edit-pane ${id ?? "md-editor"}-preview-pane`}
          className={`markdown-editor-tab ${mode === "split" ? "active" : ""}`}
          onClick={() => setMode("split")}
          disabled={!supportsPreview}
          title={supportsPreview ? "编辑 + 预览并排,滚动同步" : "Typst 类型不支持客户端预览"}
        >
          ⫶ 分屏
        </button>
        {type === "md" && (
          <span className="markdown-editor-toolbar-hint">
            支持 <code>$…$</code> / <code>$$…$$</code> KaTeX 公式 + <code>```mermaid```</code> 图表
          </span>
        )}
        {type === "bbcode" && (
          <span className="markdown-editor-toolbar-hint">
            BBCode 客户端预览 · 支持 <code>[b]</code>/<code>[i]</code>/<code>[url]</code>/<code>[img]</code> 等
          </span>
        )}
        {type === "wikidot" && (
          <span className="markdown-editor-toolbar-hint">
            Wikidot 客户端预览 · 服务器功能(include/module)显示占位符
          </span>
        )}
        {type === "html" && (
          <span className="markdown-editor-toolbar-hint">
            HTML 预览 · 经过 DOMPurify 安全过滤
          </span>
        )}
        {!supportsPreview && (
          <span className="markdown-editor-toolbar-hint">
            当前类型 ({type}) 不支持客户端预览,保存后可在文章页查看
          </span>
        )}
      </div>

      {/* Edit pane */}
      {(mode === "edit" || mode === "split") && (
        <div
          id={`${id ?? "md-editor"}-edit-pane`}
          className="markdown-editor-edit-pane"
          role="tabpanel"
        >
          <textarea
            ref={textareaRef}
            id={id}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            rows={rows}
            required={required}
            placeholder={placeholder}
            spellCheck={false}
          />
        </div>
      )}

{/* Preview pane */}
      {(mode === "preview" || mode === "split") && (
        <div
          id={`${id ?? "md-editor"}-preview-pane`}
          ref={previewCallbackRef}
          className="markdown-editor-preview-pane"
          role="tabpanel"
        >
          {!supportsPreview ? (
            <div className="markdown-editor-preview-empty">
              <p>此类型 ({type}) 暂不支持客户端预览。</p>
              <p>保存后到文章详情页查看渲染效果。</p>
            </div>
          ) : rendering && rendered === "" ? (
            <div className="markdown-editor-preview-empty">渲染中…</div>
          ) : value.trim() === "" ? (
            <div className="markdown-editor-preview-empty">
              {type === "md" && <>在左侧输入 Markdown,这里会显示渲染结果。</>}
              {type === "bbcode" && <>在左侧输入 BBCode,这里会显示渲染结果。</>}
              {type === "wikidot" && <>在左侧输入 Wikidot,这里会显示渲染结果。</>}
              {type === "html" && <>在左侧输入 HTML,这里会显示经过安全过滤的预览。</>}
            </div>
          ) : (
            <div
              className="content-body markdown-editor-preview-content"
              // eslint-disable-next-line react/no-danger
              dangerouslySetInnerHTML={{ __html: rendered }}
            />
          )}
        </div>
      )}
    </div>
  );
}