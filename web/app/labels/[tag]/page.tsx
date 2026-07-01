import { getLabelArticles } from "@/lib/api";
import { ArticleCard } from "@/components/ArticleCard";
import { Pagination } from "@/components/Pagination";
import Link from "next/link";
import type { Metadata } from "next";

export const revalidate = 60;

interface Props {
  params: Promise<{ tag: string }>;
  searchParams: Promise<{ page?: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { tag } = await params;
  return { title: `标签「${decodeURIComponent(tag)}」— 跨越晨昏` };
}

export default async function LabelPage({ params, searchParams }: Props) {
  const { tag } = await params;
  const { page: pageStr } = await searchParams;
  const page = parseInt(pageStr || "1", 10) || 1;
  const tagName = decodeURIComponent(tag);

  const result = await getLabelArticles(tagName, page).catch(() => ({
    articles: [],
    total: 0,
    page: 1,
    per_page: 10,
    total_pages: 1,
  }));

  return (
    <div className="page label-articles-page">
      <h1>
        标签「{tagName}」
      </h1>
      <Link href="/labels" className="back-link">
        ← 返回标签列表
      </Link>

      {result.articles.length === 0 ? (
        <p className="empty-message">该标签下暂无文章。</p>
      ) : (
        <>
          <p className="search-info">
            共 <strong>{result.total}</strong> 篇文章
          </p>
          <div className="article-list">
            {result.articles.map((a) => (
              <ArticleCard key={a.id} article={a} />
            ))}
          </div>
          <Pagination
            page={result.page}
            totalPages={result.total_pages}
            basePath={`/labels/${encodeURIComponent(tagName)}`}
          />
        </>
      )}
    </div>
  );
}
