import { getHome } from "@/lib/api";
import type { HomeData } from "@/lib/types";
import Link from "next/link";
import { SafeMarkdown } from "@/components/SafeMarkdown";

// ISR: revalidate homepage data every 60s so newly published/edited
// articles surface quickly while CDN caches the HTML for fast TTFB.
export const revalidate = 3600;

export default async function HomePage() {
  const home: HomeData = await getHome().catch(() => ({
    subsite_links: [],
    featured_articles: [],
    recent_articles: [],
    notifications: [],
  }));

  return (
    <div className="home-page">
      {/* 通知 */}
      <section className="home-notifications">
        {home.notifications.length > 0 ? (
          home.notifications.map((n) => (
            <div
              key={n.id}
              className={`home-notification ${n.is_important ? "important" : ""}`}
            >
              <strong>{n.title}</strong>
              <SafeMarkdown html={n.content_html} text={n.content} />
            </div>
          ))
        ) : (
          <p className="home-empty-hint">📢 暂无通知</p>
        )}
      </section>

      {/* 搜索栏 */}
      <section className="home-search">
        <form action="/search" method="GET" className="home-search-form">
          <input
            type="search"
            name="q"
            className="home-search-input"
            placeholder="搜索文章…"
            aria-label="搜索"
          />
          <button type="submit" className="btn btn-primary home-search-btn">
            🔍 搜索
          </button>
        </form>
      </section>

      {/* 推荐文章 */}
      <section className="home-featured">
        <h2 className="home-section-title">⭐ 推荐文章</h2>
        {home.featured_articles.length > 0 ? (
          <div className="featured-grid">
            {home.featured_articles.map((a) => (
              <Link
                key={a.id}
                href={`/${a.type}/${a.slug}`}
                className="featured-card"
              >
                <span className="featured-type">{a.type}</span>
                <h3>{a.title}</h3>
                <span className="featured-arrow">→</span>
              </Link>
            ))}
          </div>
        ) : (
          <p className="home-empty-hint">
            暂无推荐文章。
            <Link href="/md" className="home-browse-link">浏览全部文章</Link>
          </p>
        )}
      </section>

      {/* 最近更新 */}
      {home.recent_articles.length > 0 && (
        <section className="home-recent">
          <h2 className="home-section-title">📝 最近更新</h2>
          <div className="article-list">
            {home.recent_articles.slice(0, 5).map((a) => (
              <Link
                key={a.id}
                href={`/${a.type}/${a.slug}`}
                className="article-item"
              >
                <span className="article-type-badge">{a.type}</span>
                <div className="article-info">
                  <h3>{a.title}</h3>
                  <div className="article-meta">
                    <time>
                      {new Date(a.updated_at).toLocaleDateString("zh-CN")}
                    </time>
                    {a.tags && a.tags.length > 0 && (
                      <span className="article-tags">
                        {a.tags.map((tag: string) => (
                          <span key={tag} className="tag-badge">{tag}</span>
                        ))}
                      </span>
                    )}
                  </div>
                </div>
              </Link>
            ))}
          </div>
          {home.recent_articles.length > 5 && (
            <Link href="/md" className="home-more-link">
              查看更多 →
            </Link>
          )}
        </section>
      )}
    </div>
  );
}
