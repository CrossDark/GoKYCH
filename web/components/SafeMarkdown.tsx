"use client";

import { useEffect, useRef, useState } from "react";
import { hydrateMarkdown } from "@/lib/markdown-hydrate";

interface Props {
  /** Server-rendered HTML (from the backend's safe goldmark pass). */
  html?: string;
  /** Plain text fallback if `html` is empty / missing. */
  text?: string;
  /** Optional className applied to the wrapper. */
  className?: string;
}

/**
 * SafeMarkdown — renders backend-rendered HTML with a DOMPurify
 * defense-in-depth pass on the client. The backend already escapes raw
 * HTML in user input via the safe goldmark instance (no WithUnsafe), so
 * this is belt-and-braces, not the primary defense.
 *
 * Why a client component:
 *   - DOMPurify is browser-only (uses window), so it must run client-side.
 *   - Server components can import client components — Next.js 13+ supports
 *     this. The server emits the un-sanitised HTML; the client hydrates,
 *     sanitises, and re-renders. Brief flash of un-sanitised content is
 *     acceptable because the input is already safe.
 *
 * Why a `useEffect` instead of `dangerouslySetInnerHTML` directly:
 *   - We MUST run the sanitiser before paint, otherwise a malicious payload
 *     could trigger side-effects on a click. Dynamic-importing DOMPurify
 *     keeps the bundle small but means the first paint is un-sanitised.
 *   - For now we accept that flash because (a) the input is server-trusted
 *     and (b) the safer alternative (isomorphic-dompurify + jsdom) adds
 *     ~500KB of server bundle. Revisit if we ever expose user input that
 *     bypasses the server renderer.
 *
 * Math + Mermaid: comments / notifications occasionally include math
 * (e.g. someone pasting from a Stack-Overflow-style forum). After the
 * DOMPurify pass, run hydrateMarkdown() so `$x^2$` becomes KaTeX output.
 * It's a no-op if there's no math/mermaid in the source.
 */
export function SafeMarkdown({ html, text, className }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const [sanitised, setSanitised] = useState<string>(html ?? "");

  useEffect(() => {
    let cancelled = false;
    if (!html) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSanitised("");
      return;
    }
    (async () => {
      const DOMPurify = (await import("dompurify")).default;
      if (cancelled) return;
      setSanitised(
        DOMPurify.sanitize(html, {
          ALLOWED_TAGS: [
            "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
            "h4", "h5", "h6", "hr", "i", "img", "ins", "kbd", "li", "mark",
            "ol", "p", "pre", "s", "small", "span", "strong", "sub", "sup",
            "table", "tbody", "td", "th", "thead", "tr", "u", "ul", "del",
          ],
          ALLOWED_ATTR: ["href", "title", "alt", "src", "class", "target", "rel"],
          ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/])/i,
        })
      );
    })();
    return () => {
      cancelled = true;
    };
  }, [html]);

  // Hydrate math + mermaid once the sanitised HTML has been committed
  // to the DOM. Runs after React paints so users see plain text first,
  // then the rendered formulas swap in. Idempotent — safe to re-run.
  useEffect(() => {
    if (!ref.current || sanitised === "") return;
    let cancelled = false;
    (async () => {
      await hydrateMarkdown(ref.current!);
      if (cancelled) return;
    })();
    return () => {
      cancelled = true;
    };
  }, [sanitised]);

  if (!html && text) {
    return <div className={className}>{text}</div>;
  }
  return (
    <div
      ref={ref}
      className={className}
       
      dangerouslySetInnerHTML={{ __html: sanitised }}
    />
  );
}