// ── Auth ──────────────────────────────────────────────────────────────
export interface User {
  id: number;
  username: string;
  nickname: string;
  role: "user" | "admin" | "owner";
  avatar: string;
  bio: string;
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
  is_important: boolean;
  updated_at: string;
}

// ── Tags ─────────────────────────────────────────────────────────────
export interface TagWithCount {
  id: number;
  name: string;
  count: number;
}
