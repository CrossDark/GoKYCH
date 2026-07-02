"use client";

import { useState } from "react";
import type { Comment } from "@/lib/types";
import { SafeMarkdown } from "@/components/SafeMarkdown";
import { CommentAvatar } from "./CommentAvatar";
import { commentDisplayName } from "./commentDisplayName";
import { formatBubbleTime } from "./formatBubbleTime";

interface LineCommentBubbleProps {
  lineNum: number;
  comments: Comment[];
  top: number;
  height: number;
  onClickLine: (ln: number) => void;
}

export function LineCommentBubble({
  lineNum,
  comments,
  top,
  height,
  onClickLine,
}: LineCommentBubbleProps) {
  const [expanded, setExpanded] = useState(false);
  const sorted = [...comments].sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
  );

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
