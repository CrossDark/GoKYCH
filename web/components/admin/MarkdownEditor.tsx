"use client";

import { useEffect, useMemo, useState } from "react";

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
//                 stacks on narrow viewports
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
  placeholder = "支持 Markdown 语法…",
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
        const html = marked.parse(value || "") as string;
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
          ],
          ALLOWED_ATTR: [
            "href", "title", "alt", "src", "class", "target", "rel",
            "checked", "type", "disabled",
          ],
          ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/]|\.{1,2}\/)/i,
        });
        if (!cancelled) setRendered(safe);
      } finally {
        if (!cancelled) setRendering(false);
      }
    }, 120); // ~120ms debounce — fast enough to feel live, slow enough to skip during fast typing
    return () => {
      cancelled = true;
      clearTimeout(handle);
    };
  }, [value, mode, supportsPreview]);

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
          title={supportsPreview ? "编辑 + 预览并排" : "仅 Markdown 类型支持预览"}
        >
          ⫶ 分屏
        </button>
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