import Link from "next/link";
import type { Article } from "@/lib/types";

export function ArticleCard({ article }: { article: Article }) {
  return (
    <Link href={`/${article.type}/${article.slug}`} className="article-item">
      <span className="article-type-badge">{article.type}</span>
      <div className="article-info">
        <h3>{article.title}</h3>
        <div className="article-meta">
          <time>{new Date(article.updated_at).toLocaleDateString("zh-CN")}</time>
          {(article.tags?.length ?? 0) > 0 && (
            <span className="article-tags">
              {article.tags.map((tag) => (
                <span key={tag} className="tag-badge">{tag}</span>
              ))}
            </span>
          )}
        </div>
      </div>
    </Link>
  );
}
