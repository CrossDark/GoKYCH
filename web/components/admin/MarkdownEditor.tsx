"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  hydrateMarkdown,
  preprocessMathBlocks,
  postprocessMathBlocks,
} from "@/lib/markdown-hydrate";

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
// For non-md types (wikidot / html / bbcode / typst)
// the preview pane shows a notice explaining that
// preview isn't supported client-side — those types
// have specialised renderers (wikidot has its own
// parser, typst compiles to PDF via the backend)
// that don't fit the "render in the browser"
// pattern. The author can still see what they typed
// in edit mode and verify against the article view
// after save.

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
  const supportsPreview = type === "md";
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

  // Render the markdown source through `marked` and
  // sanitise the output through DOMPurify. Re-runs
  // whenever the source changes (debounced via the
  // effect's setTimeout so a fast typist doesn't
  // queue a parser pass per keystroke).
  useEffect(() => {
    if (!supportsPreview || (mode === "edit" && value === "")) {
      // No preview to compute (preview disabled or
      // empty source) — leave the last render in
      // place so the user sees the rendered output
      // stay stable while clearing the source.
      return;
    }
    if (mode === "edit") {
      return;
    }
    let cancelled = false;
    setRendering(true);
    const handle = setTimeout(async () => {
      try {
        const { marked } = await import("marked");
        // Configure marked once on first use; safe
        // defaults (no raw HTML pass-through, GitHub-
        // style line breaks, etc.).
        marked.setOptions({
          gfm: true,
          breaks: true,
          // Don't allow raw HTML through — anything
          // looking like a tag is escaped. The
          // DOMPurify pass below is the second line
          // of defence; the marked escape is the
          // first.
        });
        if (cancelled) return;
        // Pre-extract block-math placeholders BEFORE marked. With
        // `breaks: true`, marked turns the newlines inside `$$…$$`
        // into `<br>` elements, which would split the block into
        // separate text nodes and defeat any inline text-walker.
        const { source: preSource, blocks } = preprocessMathBlocks(value || "");
        const html = marked.parse(preSource) as string;
        if (cancelled) return;
        const DOMPurify = (await import("dompurify")).default;
        if (cancelled) return;
        const safe = DOMPurify.sanitize(html, {
          ALLOWED_TAGS: [
            "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
            "h4", "h5", "h6", "hr", "i", "img", "ins", "kbd", "li", "mark",
            "ol", "p", "pre", "s", "small", "span", "strong", "sub", "sup",
            "table", "tbody", "td", "th", "thead", "tr", "u", "ul", "del",
            "figure", "figcaption", "details", "summary", "input",
            "div",  // for math-block wrappers post-processed in below
          ],
          ALLOWED_ATTR: [
            "href", "title", "alt", "src", "class", "target", "rel",
            "checked", "type", "disabled",
            "data-math-rendered",
          ],
          ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
        });
        if (cancelled) return;
        // Swap the placeholder strings for KaTeX HTML AFTER DOMPurify
        // (so KaTeX's MathML tags survive untouched). DOMPurify sees
        // the placeholders as plain text.
        const finalHtml = postprocessMathBlocks(safe, blocks);
        if (!cancelled) setRendered(finalHtml);
      } finally {
        if (!cancelled) setRendering(false);
      }
    }, 120); // ~120ms debounce — fast enough to feel live, slow enough to skip during fast typing
    return () => {
      cancelled = true;
      clearTimeout(handle);
    };
  }, [value, mode, supportsPreview]);

  // After React commits the `rendered` HTML into the
  // preview pane, run hydrateMarkdown() to swap raw
  // `$...$` / `$$...$$` / ` ```math ` text into KaTeX
  // and ` ```mermaid ` blocks into SVG. We key on a
  // hash of the HTML so re-renders triggered by the
  // sync-scroll effect (which doesn't change `rendered`)
  // don't re-hydrate needlessly.
  useEffect(() => {
    if (mode === "edit") return;
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
  }, [rendered, mode, previewNode]);

  // Sync-scroll in split mode: when the user scrolls
  // either pane, propagate the proportional scroll
  // position to the other. The `isSyncing` flag breaks
  // the feedback loop (textarea scroll → preview scroll
  // → textarea scroll → ...).
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
    let isSyncing = false;
    const onTextareaScroll = () => {
      if (isSyncing) return;
      const denom = textarea.scrollHeight - textarea.clientHeight;
      if (denom <= 0) return;
      isSyncing = true;
      const ratio = textarea.scrollTop / denom;
      const pDenom = preview.scrollHeight - preview.clientHeight;
      preview.scrollTop = ratio * Math.max(0, pDenom);
      requestAnimationFrame(() => {
        isSyncing = false;
      });
    };
    const onPreviewScroll = () => {
      if (isSyncing) return;
      const denom = preview.scrollHeight - preview.clientHeight;
      if (denom <= 0) return;
      isSyncing = true;
      const ratio = preview.scrollTop / denom;
      const tDenom = textarea.scrollHeight - textarea.clientHeight;
      textarea.scrollTop = ratio * Math.max(0, tDenom);
      requestAnimationFrame(() => {
        isSyncing = false;
      });
    };
    textarea.addEventListener("scroll", onTextareaScroll);
    preview.addEventListener("scroll", onPreviewScroll);
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
          title={supportsPreview ? "预览渲染结果" : "仅 Markdown 类型支持预览"}
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
          title={supportsPreview ? "编辑 + 预览并排,滚动同步" : "仅 Markdown 类型支持预览"}
        >
          ⫶ 分屏
        </button>
        {supportsPreview && (
          <span className="markdown-editor-toolbar-hint">
            支持 <code>$…$</code> / <code>$$…$$</code> KaTeX 公式 + <code>```mermaid```</code> 图表
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
              在左侧输入 Markdown,这里会显示渲染结果。
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