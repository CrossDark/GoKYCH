// ── Auth ──────────────────────────────────────────────────────────────
export interface User {
  id: number;
  username: string;
  nickname: string;
  role: "user" | "admin" | "owner";
  avatar: string;
  bio: string;
  /** Owner's contact email. Empty string when not set (server stores NULL
   *  as "" after JSON marshal — `NULLIF('', '')` in the backend). */
  social_email: string;
  /** Owner's GitHub handle or profile URL. */
  social_github: string;
  /** Owner's QQ number (digits, no UI rendering — just the value). */
  social_qq: string;
  created_at: string;
}

export interface LoginResponse {
  status: string;
  user: User;
  next: string;
  message: string;
}

export interface CaptchaResponse {
  csrf_token: string;
  captcha: { question: string };
}

// ── Articles ─────────────────────────────────────────────────────────
export interface Article {
  id: number;
  type: string;
  slug: string;
  title: string;
  content: string;
  author_id: number | null;
  /** Username of the author (LEFT-JOINed from users; empty for anonymous /
   *  deleted-author rows). Use this to decide whether to show the author
   *  block at all. */
  author_name?: string;
  author_nickname?: string;
  author_avatar?: string;
  tags: string[];
  created_at: string;
  updated_at: string;
}

export interface ArticleListResult {
  articles: Article[];
  total: number;
  page: number;
  per_page: number;
  total_pages: number;
}

export interface ArticleDetail {
  article: Article;
  html: string;
  tags: string[];
  comments: Comment[];
  line_comments: LineComment[];
  line_comment_counts: Record<number, number>;
  rating: RatingSummary | null;
  can_edit: boolean;
}

// ── Comments ─────────────────────────────────────────────────────────
export interface Comment {
  id: number;
  article_id: number;
  line_number: number | null;
  author_name: string;
  content: string;
  /** Server-rendered, sanitized markdown HTML. Use for display; the
   *  frontend should still pass this through DOMPurify as defense in depth. */
  content_html?: string;
  created_at: string;
}

export type LineComment = Comment;

// ── Ratings ──────────────────────────────────────────────────────────
export interface RatingSummary {
  average_score: number;
  total_voters: number;
  user_score: number | null;
}

export interface RatingDetail {
  id: number;
  article_id: number;
  author_name: string;
  score: number;
  created_at: string;
}

// ── Home ─────────────────────────────────────────────────────────────
export interface HomeData {
  subsite_links: SubsiteLink[];
  featured_articles: FeaturedArticle[];
  recent_articles: Article[];
  notifications: Notification[];
}

export interface SubsiteLink {
  name: string;
  url: string;
  description: string;
}

export interface FeaturedArticle {
  id: number;
  type: string;
  slug: string;
  title: string;
}

export interface Notification {
  id: number;
  title: string;
  content: string;
  /** Server-rendered, sanitized markdown HTML. */
  content_html?: string;
  is_important: boolean;
  is_active?: boolean;
  updated_at: string;
}

// ── Tags ─────────────────────────────────────────────────────────────
export interface TagWithCount {
  id: number;
  name: string;
  count: number;
}

// ── Theme ─────────────────────────────────────────────────────────────
//
// Returned by GET /api/themes. Theme CSS itself is loaded directly from
// /api/themes/:name.css (text/css, no JSON wrapping).
export interface Theme {
  name: string;
  version?: string;
  author?: string;
  description?: string;
  has_css: boolean;
}

// ── Site config ───────────────────────────────────────────────────────
//
// Returned by GET /api/site. The frontend (Header, LayoutWrapper, footer)
// reads this once at mount to render title/subtitle/theme/ICP/subsite_links.
export interface SiteConfig {
  site: {
    title: string;
    subtitle: string;
    description: string;
    language: string;
    timezone: string;
    logo_path: string;
    favicon_path: string;
    icp_number: string;
  };
  appearance: {
    font_family: string;
    primary_color: string;
    style_theme: string;
    theme: string;
  };
  features: {
    enable_comments: boolean;
    enable_dark_mode: boolean;
    enable_search: boolean;
    enable_tags_sidebar: boolean;
    posts_per_page: number;
  };
  // NOTE: site-level `social` was removed — per-user social links now
  // live on `User.social_email / social_github / social_qq` and are
  // exposed via /api/admin/profile.
  subsite_links: SubsiteLink[];
}

// ── Admin: Tags ───────────────────────────────────────────────────────
export interface AdminTag {
  id: number;
  name: string;
  count: number;
}

// ── Admin: Files ──────────────────────────────────────────────────────
export interface AdminFile {
  id: number;
  filename: string;
  original_name: string;
  file_size: number;
  mime_type: string;
  created_at: string;
  // Absolute public URL of the file (PUBLIC_URL + /uploads/<filename> in
  // production). Use this in <img src> / <a href> so cross-origin
  // deployments (CF Pages + separate API host) resolve correctly.
  // Falls back to a relative /uploads/<filename> in dev (where the
  // Next.js rewrite handles it).
  url: string;
}
