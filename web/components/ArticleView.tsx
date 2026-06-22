"use client";

import Link from "next/link";
import type { ArticleDetail } from "@/lib/types";
import { RatingWidget } from "./RatingWidget";
import { CommentSection } from "./CommentSection";

interface Props {
  data: ArticleDetail;
  articleType: string;
  articleSlug: string;
}

function splitLines(html: string): string[] {
  return html.split(/\n|<br\s*\/?>/i);
}

export function ArticleView({ data, articleType, articleSlug }: Props) {
  const { article, html, rating, comments: rawComments, line_comment_counts: rawLineCounts, can_edit } = data;
  const comments = rawComments ?? [];
  const line_comment_counts = rawLineCounts ?? {};
  const lines = splitLines(html ?? "");

  return (
    <article className="article-detail">
      {/* Header */}
      <header className="article-header">
        <div className="article-type-row">
          <span className="article-type-badge">{article.type}</span>
          <div className="article-tags">
            {article.tags.map((tag) => (
              <Link key={tag} href={`/labels/${tag}`} className="tag-badge">
                {tag}
              </Link>
            ))}
          </div>
        </div>
        <h1 className="article-title">{article.title}</h1>
        <div className="article-meta">
          <time>
            发布于 {new Date(article.created_at).toLocaleDateString("zh-CN")}
          </time>
          {article.created_at !== article.updated_at && (
            <time className="updated-at">
              · 更新于 {new Date(article.updated_at).toLocaleDateString("zh-CN")}
            </time>
          )}
          {can_edit && (
            <Link href={`/admin/articles/${article.type}/${article.slug}`} className="edit-link">
              编辑
            </Link>
          )}
        </div>
      </header>

      {/* Rating */}
      {rating && (
        <RatingWidget
          articleType={articleType}
          articleSlug={articleSlug}
          initialAvg={rating.average_score}
          initialVoters={rating.total_voters}
          initialUserScore={rating.user_score}
        />
      )}

      {/* Content with line comments */}
      <div className="article-content">
        {lines.map((line, i) => (
          <div key={i} className="article-line" id={`line-${i + 1}`}>
            <span className="line-number" data-line={i + 1}>
              {line_comment_counts[i + 1] ? (
                <span className="line-comment-count">
                  {line_comment_counts[i + 1]}
                </span>
              ) : (
                <span className="line-number-text">{i + 1}</span>
              )}
            </span>
            <div
              className="line-content"
              dangerouslySetInnerHTML={{ __html: line || "&nbsp;" }}
            />
          </div>
        ))}
      </div>

      {/* Comments */}
      <CommentSection
        articleType={articleType}
        articleSlug={articleSlug}
        initialComments={comments}
      />
    </article>
  );
}
