import { getHome } from "@/lib/api";
import type { HomeData } from "@/lib/types";
import Link from "next/link";

export const dynamic = "force-dynamic";

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
      {home.notifications.length > 0 && (
        <section className="notifications">
          {home.notifications.map((n) => (
            <div
              key={n.id}
              className={`notification ${n.is_important ? "important" : ""}`}
            >
              <strong>{n.title}</strong>
              <span>{n.content}</span>
            </div>
          ))}
        </section>
      )}

      {/* 子站点链接 */}
      {home.subsite_links.length > 0 && (
        <section className="subsite-links">
          <div className="subsite-grid">
            {home.subsite_links.map((link) => (
              <a
                key={link.name}
                href={link.url}
                className="subsite-card"
                target="_blank"
                rel="noopener noreferrer"
              >
                <h3>{link.name}</h3>
                {link.description && <p>{link.description}</p>}
              </a>
            ))}
          </div>
        </section>
      )}

      {/* 推荐文章 */}
      {home.featured_articles.length > 0 && (
        <section className="section featured-section">
          <h2 className="section-title">推荐文章</h2>
          <div className="featured-grid">
            {home.featured_articles.map((a) => (
              <Link
                key={a.id}
                href={`/${a.type}/${a.slug}`}
                className="featured-card"
              >
                <div className="featured-type">{a.type}</div>
                <h3>{a.title}</h3>
              </Link>
            ))}
          </div>
        </section>
      )}

      {/* 最近文章 */}
      <section className="section recent-section">
        <h2 className="section-title">最近更新</h2>
        {home.recent_articles.length === 0 ? (
          <p className="empty-message">暂无文章。</p>
        ) : (
          <div className="article-list">
            {home.recent_articles.map((a) => (
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
        )}
      </section>
    </div>
  );
}
