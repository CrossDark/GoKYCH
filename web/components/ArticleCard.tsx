import Link from "next/link";
import type { Article } from "@/lib/types";
import { UserAvatar } from "@/components/admin/UserAvatar";

export function ArticleCard({ article }: { article: Article }) {
  const hasAuthor = !!article.author_name;
  return (
    <Link href={`/${article.type}/${article.slug}`} className="article-item">
      <span className="article-type-badge">{article.type}</span>
      <div className="article-info">
        <h3>{article.title}</h3>
        <div className="article-meta">
          {hasAuthor && (
            <span className="article-author">
              <UserAvatar
                user={{
                  nickname: article.author_nickname || "",
                  username: article.author_name || "",
                  avatar: article.author_avatar || "",
                }}
                size={22}
              />
              <span>{article.author_nickname || article.author_name}</span>
            </span>
          )}
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
