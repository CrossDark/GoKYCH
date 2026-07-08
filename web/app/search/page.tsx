"use client";

import { useState, useEffect, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { search } from "@/lib/api";
import { ArticleCard } from "@/components/ArticleCard";
import { Pagination } from "@/components/Pagination";
import type { ArticleListResult } from "@/lib/types";

function SearchForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const q = searchParams.get("q") || "";
  const pageStr = searchParams.get("page") || "1";
  const page = parseInt(pageStr, 10) || 1;

  const [query, setQuery] = useState(q);
  const [result, setResult] = useState<ArticleListResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [searched, setSearched] = useState(false);

  const doSearch = async (p: number) => {
    if (!q.trim()) return;
    setLoading(true);
    setError("");
    try {
      const r = await search(q, p);
      setResult(r);
      setSearched(true);
    } catch (err: any) {
      setError(err.message || "搜索失败。");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (q) doSearch(page);
  }, [q, page]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = query.trim();
    if (!trimmed) return;
    router.push(`/search?q=${encodeURIComponent(trimmed)}`);
  };

  return (
    <div className="page search-page">
      <h1>搜索</h1>
      <form className="search-form" onSubmit={handleSubmit}>
        <input
          type="search"
          className="search-input"
          placeholder="搜索文章…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button type="submit" className="btn btn-primary" disabled={loading}>
          搜索
        </button>
      </form>

      {error && <p className="form-error">{error}</p>}

      {loading && <p className="loading">搜索中…</p>}

      {searched && result && (
        <>
          <p className="search-summary">找到 {result.total} 条结果</p>
          {result.articles.length === 0 ? (
            <p className="empty-message">未找到匹配的文章。</p>
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
            basePath={`/search?q=${encodeURIComponent(q)}`}
          />
        </>
      )}
	    </div>
	  );
	}

export default function SearchPage() {
  return (
    <Suspense fallback={<div className="page search-page"><h1>搜索</h1><p>加载中…</p></div>}>
      <SearchForm />
    </Suspense>
  );
}
