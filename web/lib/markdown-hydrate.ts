// ── Markdown hydration helpers ─────────────────────────────────────────
//
// `marked` (client-side) and Goldmark (server-side) BOTH leave math
// and mermaid blocks alone — neither parses `$x^2$` / `$$x^2$$` /
// `\[x^2\]` / ` ```mermaid `.
//
// The hydrators below convert those raw patterns into KaTeX HTML /
// mermaid SVG. Three concerns shaped the design:
//
// 1. Math blocks ($$...$$, \[...\]) often span MULTIPLE LINES.
//    marked with `breaks: true` (the editor's setting) inserts `<br>`
//    between every line, which splits the block into separate text
//    nodes. A naive walker can't match a regex across `<br>`s.
//
//    → Solution: a pre-processor that extracts `$$...$$` and `\[...\]`
//    into numbered placeholders BEFORE marked parses. After marked
//    + DOMPurify, post-processor swaps the placeholder strings with
//    KaTeX HTML (rendered via renderToString so we work on strings,
//    not DOM nodes — DOMPurify then sees plain text placeholders).
//
// 2. KaTeX HTML uses MathML tags (`<math>`, `<semantics>`, …) that
//    DOMPurify would strip.
//
//    → Solution: render KaTeX AFTER DOMPurify, then patch the result
//    back into the HTML string just before `dangerouslySetInnerHTML`.
//
// 3. Mermaid is ~600KB and needs a DOM. We lazy-load it on first use
//    so the editor's first paint isn't blocked.

import katex from "katex";

// ── Pre-process: extract block-math placeholders ───────────────────
//
// Two syntaxes supported at the block level:
//   - `$$…$$` (GitHub-style, multi-line OK)
//   - `\[…\]` (LaTeX-style, multi-line OK)
//
// Inline `$…$` and `\(…\)` are handled by the text-walker after
// marked — they're always single-line so `<br>` doesn't split them.
export interface MathBlock {
  type: "block" | "display-latex";
  tex: string;
}

export function preprocessMathBlocks(source: string): {
  source: string;
  blocks: MathBlock[];
} {
  const blocks: MathBlock[] = [];
  let i = 0;
  // Run display-latex first because `\[` and `\]` would be eaten by
  // `$$` matching otherwise (well, not really — `\`` is a backslash —
  // but doing the more specific pattern first feels right).
  const out = source
    .replace(/\\\[([\s\S]+?)\\\]/g, (_, tex: string) => {
      blocks.push({ type: "display-latex", tex });
      return `@@MATHBLOCK${i++}@@`;
    })
    .replace(/\$\$([\s\S]+?)\$\$/g, (_, tex: string) => {
      blocks.push({ type: "block", tex });
      return `@@MATHBLOCK${i++}@@`;
    });
  return { source: out, blocks };
}

/**
 * Swap `@@MATHBLOCK<N>@@` placeholders in `html` for KaTeX HTML.
 * Runs AFTER DOMPurify so KaTeX's MathML tags survive untouched.
 */
export function postprocessMathBlocks(html: string, blocks: MathBlock[]): string {
  let out = html;
  for (let i = 0; i < blocks.length; i++) {
    const ph = `@@MATHBLOCK${i}@@`;
    const block = blocks[i];
    let rendered: string;
    try {
      rendered = katex.renderToString(block.tex, {
        displayMode: true,
        throwOnError: false,
        output: "html",
        trust: false,
      });
    } catch {
      // Should never happen with throwOnError:false, but be safe.
      rendered = `<span data-math-error="true">${escapeHtml(block.tex)}</span>`;
    }
    // Wrap in a div so CSS can target it and so the placeholder text
    // (which may sit inside a `<p>`) doesn't leave a stray empty `<p>`
    // when replaced.
    const wrapped = `<div class="math-block" data-math-rendered="${block.type}">${rendered}</div>`;
    out = out.split(ph).join(wrapped);
  }
  return out;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// ── Text-walker: math in text nodes ($…$, \(…\), $$…$$, \[…\]) ────
//
// Runs after marked + DOMPurify. Walks text nodes outside of
// <code>/<pre>/<a>/<script>/<style>/.katex and replaces math patterns
// with KaTeX HTML.
//
// Block patterns ($$…$$, \[…\]) ONLY work here when they happen to
// live in a single text node — Goldmark (server-side) emits them
// this way, but `marked` with `breaks: true` (editor preview) splits
// them across multiple nodes via <br>. The editor preview therefore
// uses `preprocessMathBlocks` + `postprocessMathBlocks` to extract
// block patterns before parsing; this walker is the fallback for the
// public article view, where the server already gave us a clean DOM.
type MathMatch = {
  start: number;
  end: number;
  type: "inline" | "block";
  tex: string;
};

function isInsideForbiddenContext(el: Element | null): boolean {
  if (!el) return false;
  return !!el.closest(
    "code, pre, a, script, style, .katex, [data-math-rendered]",
  );
}

function processTextNodeForMath(text: Text): DocumentFragment | null {
  const source = text.nodeValue;
  if (!source || (!source.includes("$") && !source.includes("\\"))) return null;

  const matches: MathMatch[] = [];

  // Block math — `$$…$$`.
  const blockRe = /\$\$([^$]+?)\$\$(?!\$)/g;
  let m: RegExpExecArray | null;
  while ((m = blockRe.exec(source))) {
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      type: "block",
      tex: m[1],
    });
  }

  // Display math — `\[…\]`.
  const displayRe = /\\\[([\s\S]+?)\\\]/g;
  while ((m = displayRe.exec(source))) {
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      type: "block",
      tex: m[1],
    });
  }

  // Inline math — `$…$` (no newlines inside).
  const inlineRe = /\$([^$\n]+?)\$(?!\$)/g;
  while ((m = inlineRe.exec(source))) {
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      type: "inline",
      tex: m[1],
    });
  }

  // Inline math — `\(…\)` (LaTeX-style).
  const inlineParenRe = /\\\(([\s\S]+?)\\\)/g;
  while ((m = inlineParenRe.exec(source))) {
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      type: "inline",
      tex: m[1],
    });
  }

  if (matches.length === 0) return null;

  // Sort by start, drop overlaps (first match wins).
  matches.sort((a, b) => a.start - b.start);
  const filtered: MathMatch[] = [];
  let lastEnd = -1;
  for (const mt of matches) {
    if (mt.start >= lastEnd) {
      filtered.push(mt);
      lastEnd = mt.end;
    }
  }
  if (filtered.length === 0) return null;

  const frag = document.createDocumentFragment();
  let cursor = 0;
  for (const mt of filtered) {
    if (mt.start > cursor) {
      frag.appendChild(document.createTextNode(source.slice(cursor, mt.start)));
    }
    if (mt.type === "block") {
      // Block math — render in display mode, wrap in a div so it can
      // sit comfortably inside an existing <p> (the browser will
      // accept the div; some CSS may close the <p> first depending
      // on context, but the visual result is fine).
      const div = document.createElement("div");
      div.className = "math-block";
      div.setAttribute("data-math-rendered", "block-walker");
      try {
        katex.render(mt.tex, div, {
          displayMode: true,
          throwOnError: false,
          output: "html",
          trust: false,
        });
      } catch {
        div.textContent = `$$${mt.tex}$$$`;
        div.setAttribute("data-math-error", "true");
      }
      frag.appendChild(div);
    } else {
      const span = document.createElement("span");
      span.setAttribute("data-math-rendered", "inline");
      try {
        katex.render(mt.tex, span, {
          displayMode: false,
          throwOnError: false,
          output: "html",
          trust: false,
        });
      } catch {
        span.textContent = `$${mt.tex}$`;
        span.setAttribute("data-math-error", "true");
      }
      frag.appendChild(span);
    }
    cursor = mt.end;
  }
  if (cursor < source.length) {
    frag.appendChild(document.createTextNode(source.slice(cursor)));
  }
  return frag;
}

/**
 * Walk `root` and replace math patterns in text nodes with KaTeX
 * output. Safe to call multiple times — already-rendered spans are
 * skipped via the `data-math-rendered` attribute.
 */
export function hydrateMath(root: HTMLElement): void {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = (node as Text).parentElement;
      if (isInsideForbiddenContext(parent)) {
        return NodeFilter.FILTER_REJECT;
      }
      const v = (node as Text).nodeValue;
      if (!v || (!v.includes("$") && !v.includes("\\"))) {
        return NodeFilter.FILTER_REJECT;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });

  const targets: Text[] = [];
  let n: Node | null;
  while ((n = walker.nextNode())) targets.push(n as Text);

  for (const text of targets) {
    const parent = text.parentElement;
    if (!parent) continue;
    const frag = processTextNodeForMath(text);
    if (frag) parent.replaceChild(frag, text);
  }
}

// ── Fenced math code block hydrator ────────────────────────────────
//
// Handles ` ```math ` fenced code blocks — a Markdown-extension
// syntax recognised by neither marked nor Goldmark by default, but
// commonly used in academic write-ups. Both produce identical
// `<pre><code class="language-math">…</code></pre>` HTML so this
// hydrator works on both editor preview and server-side renders.
export function hydrateMathCodeBlocks(root: HTMLElement): void {
  const codes = root.querySelectorAll<HTMLElement>(
    "pre > code.language-math",
  );
  codes.forEach((code) => {
    if (code.dataset.mathCodeRendered === "true") return;
    const pre = code.parentElement;
    if (!pre || pre.tagName !== "PRE") return;
    const tex = code.textContent ?? "";
    const div = document.createElement("div");
    div.className = "math-block";
    div.setAttribute("data-math-rendered", "block-code");
    try {
      katex.render(tex, div, {
        displayMode: true,
        throwOnError: false,
        output: "html",
        trust: false,
      });
    } catch {
      div.textContent = tex;
    }
    pre.replaceWith(div);
  });
}

// ── Mermaid hydrator ───────────────────────────────────────────────
let mermaidPromise: Promise<typeof import("mermaid").default> | null = null;

async function getMermaid(): Promise<typeof import("mermaid").default> {
  if (!mermaidPromise) {
    mermaidPromise = (async () => {
      const mermaidMod = await import("mermaid");
      const mermaid = mermaidMod.default;
      mermaid.initialize({
        startOnLoad: false,
        theme: "default",
        securityLevel: "strict",
        fontFamily: "inherit",
      });
      return mermaid;
    })();
  }
  return mermaidPromise;
}

export async function hydrateMermaid(root: HTMLElement): Promise<void> {
  const codes = root.querySelectorAll<HTMLElement>(
    "pre > code.language-mermaid",
  );
  if (codes.length === 0) return;

  const mermaid = await getMermaid();
  let i = 0;
  for (const code of codes) {
    if (code.dataset.mermaidRendered === "true") continue;
    const pre = code.parentElement;
    if (!pre || pre.tagName !== "PRE") continue;
    const source = (code.textContent ?? "").trim();
    const id = `mermaid-${Date.now()}-${i++}`;
    try {
      const { svg } = await mermaid.render(id, source);
      const wrapper = document.createElement("div");
      wrapper.className = "mermaid-diagram";
      wrapper.setAttribute("data-mermaid-rendered", "true");
      wrapper.innerHTML = svg;
      pre.replaceWith(wrapper);
    } catch (e) {
      const err = e as Error;
      const errBox = document.createElement("div");
      errBox.className = "mermaid-error";
      errBox.setAttribute("data-mermaid-error", "true");
      errBox.textContent = `Mermaid 渲染失败: ${err.message ?? err}`;
      pre.replaceWith(errBox);
    }
  }
}

/**
 * Run all DOM-side hydrators. Math is synchronous; mermaid is async
 * because of the dynamic import.
 *
 * Note: this does NOT handle the block-math placeholders
 * (`$$...$$` / `\[...\]`) — those are pre/post-processed at the
 * string level by `preprocessMathBlocks` / `postprocessMathBlocks`
 * because marked's `breaks: true` splits them across text nodes.
 * This walker handles the inline cases (`$...$`, `\(...\)`) and the
 * fenced ` ```math ` blocks.
 */
export async function hydrateMarkdown(root: HTMLElement): Promise<void> {
  hydrateMathCodeBlocks(root);
  hydrateMath(root);
  await hydrateMermaid(root);
}