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
  author_nickname?: string;
  author_avatar?: string;
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
  /** true for built-in themes shipped with GoKYCH (cannot be deleted). */
  builtin: boolean;
  updated_at?: string;
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
    allow_all_edit: boolean;
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

// ── Admin: Sidebar cards ──────────────────────────────────────────────
// Admin-managed cards rendered inside the front-end <SideDrawer> (the
// ☰ drawer to the left of the site name). Distinct from AdminTag —
// the two drawers serve different intents (cards = site nav, tags =
// article index).
export interface AdminSidebarCard {
  id: number;
  title: string;
  url: string;
  icon: string;
  description: string;
  sort_order: number;
  is_external: boolean;
  is_active: boolean;
}

// ── Admin: Files ──────────────────────────────────────────────────────
export interface AdminFile {
  id: number;
  filename: string;
  original_name: string;
  file_size: number;
  mime_type: string;
  created_at: string;
  url: string;
}

// ── Admin: Site Settings ──────────────────────────────────────────────
export interface SiteSettings {
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
    allow_all_edit: boolean;
  };
  subsite_links: SubsiteLink[];
}

// ── Admin: API Keys ───────────────────────────────────────────────────
export interface ApiKey {
  id: number;
  owner_id: number;
  name: string;
  key_prefix: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  created_at: string;
}

export interface CreateApiKeyResponse extends ApiKey {
  plaintext_key?: string;
  warning?: string;
}

// ── Admin: Passkeys ───────────────────────────────────────────────────
export interface PasskeyInfo {
  id: number;
  user_id: number;
  user_name: string;
  user_nickname: string;
  name: string;
  credential_id: string;
  transports: string[];
  sign_count: number;
  created_at: string;
}

export interface MyPasskeyInfo {
  id: number;
  name: string;
  credential_id: string;
  transports: string[];
  sign_count: number;
  created_at: string;
}

// ── Admin: System Update ──────────────────────────────────────────────
export interface UpdateCheckInfo {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  platform: string;
  os: string;
  arch: string;
  binary_path: string;
  can_write: boolean;
  can_write_error?: string;
  write_err_category?: "erofs" | "eacces" | "eperm" | "other" | string;
  process_user?: string;
  dir_permissions?: string;
  in_container?: boolean;
  mount_options?: string;
  published_at?: string;
  release_url?: string;
  release_notes?: string;
  download_size?: number;
  error?: string;
}

export type UpdatePhase = "idle" | "downloading" | "verifying" | "replacing" | "restarting" | "done" | "error";

export interface UpdateStatus {
  status: UpdatePhase;
  version?: string;
  message: string;
  error?: string;
  backup?: string;
  progress: number;
  total: number;
  elapsed_sec: number;
}
