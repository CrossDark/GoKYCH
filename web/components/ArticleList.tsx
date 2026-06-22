import { listArticles } from "@/lib/api";
import { ArticleCard } from "./ArticleCard";
import { Pagination } from "./Pagination";

interface Props {
  type: string;
  page: number;
  title: string;
}

export default async function ArticleList({ type, page, title }: Props) {
  const result = await listArticles(type, page).catch(() => ({
    articles: [],
    total: 0,
    page: 1,
    per_page: 10,
    total_pages: 1,
  }));

  return (
    <div className="page article-list-page">
      <h1>{title}</h1>
      {result.articles.length === 0 ? (
        <p className="empty-message">暂无文章。</p>
      ) : (
        <div className="article-list">
          {result.articles.map((a) => (
            <ArticleCard key={a.id} article={a} />
          ))}
        </div>
      )}
      <Pagination
        page={result.page}
        totalPages={result.total_pages}
        basePath={`/${type}`}
      />
    </div>
  );
}
