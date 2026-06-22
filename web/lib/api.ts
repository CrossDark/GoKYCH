const BASE =
  typeof window === "undefined"
    ? process.env.API_BASE_URL || "http://localhost:8000"
    : "";

async function request<T>(
  path: string,
  options?: RequestInit
): Promise<T> {
  const res = await fetch(`${BASE}/api${path}`, {
    ...(typeof window !== "undefined" ? { credentials: "include" } : {}),
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...options?.headers,
    },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || "请求失败");
  }
  return res.json();
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// ── Auth ──────────────────────────────────────────────────────────
export function getMe() {
  return request<{ user: import("./types").User | null }>("/auth/me");
}

export function getCsrf() {
  return request<import("./types").CaptchaResponse>("/auth/csrf");
}

export function login(body: {
  username: string;
  password: string;
  captcha: string;
  csrf_token: string;
}) {
  return request<import("./types").LoginResponse>("/auth/login", {
    method: "POST",
    headers: { "X-CSRF-Token": body.csrf_token },
    body: JSON.stringify(body),
  });
}

export function logout(csrfToken: string) {
  return request<{ status: string }>("/auth/logout", {
    method: "POST",
    headers: { "X-CSRF-Token": csrfToken },
  });
}

// ── Articles ──────────────────────────────────────────────────────
export function listArticles(type?: string, page = 1) {
  const q = new URLSearchParams();
  if (type) q.set("type", type);
  q.set("page", String(page));
  return request<import("./types").ArticleListResult>(
    `/articles?${q.toString()}`
  );
}

export function getArticle(type: string, slug: string) {
  return request<import("./types").ArticleDetail>(
    `/articles/${type}/${slug}`
  );
}

export function createArticle(
  type: string,
  csrf: string,
  body: { slug: string; title: string; content: string; tags?: string[] }
) {
  return request<import("./types").Article>(
    `/articles?type=${type}`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function updateArticle(
  type: string,
  slug: string,
  csrf: string,
  body: { title: string; content: string; tags?: string[] }
) {
  return request<import("./types").Article>(
    `/articles/${type}/${slug}`,
    {
      method: "PUT",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

export function deleteArticle(type: string, slug: string, csrf: string) {
  return request<{ status: string }>(
    `/articles/${type}/${slug}`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

// ── Comments ──────────────────────────────────────────────────────
export function addComment(
  type: string, slug: string, csrf: string,
  body: { content: string; author_name?: string }
) {
  return request<import("./types").Comment>(
    `/articles/${type}/${slug}/comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

// ── Rating ────────────────────────────────────────────────────────
export function setRating(
  type: string, slug: string, csrf: string, score: number
) {
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify({ score }),
    }
  );
}

// ── Home ──────────────────────────────────────────────────────────
export function getHome() {
  return request<import("./types").HomeData>("/home");
}

// ── Labels ────────────────────────────────────────────────────────
export function listLabels() {
  return request<import("./types").TagWithCount[]>("/labels");
}

export function getLabelArticles(tag: string, page = 1) {
  return request<import("./types").ArticleListResult>(
    `/labels/${encodeURIComponent(tag)}?page=${page}`
  );
}

// ── Search ────────────────────────────────────────────────────────
export function search(q: string, page = 1) {
  return request<import("./types").ArticleListResult>(
    `/search?q=${encodeURIComponent(q)}&page=${page}`
  );
}

// ── Admin: Users ──────────────────────────────────────────────────
export function listUsers(csrf: string) {
  return request<import("./types").User[]>("/admin/users", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function createUser(csrf: string, body: { username: string; password: string; nickname?: string; role?: string }) {
  return request<import("./types").User>("/admin/users", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function updateUserRole(csrf: string, username: string, role: string) {
  return request<{ status: string }>(`/admin/users/${username}/role`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ role }),
  });
}

export function deleteUser(csrf: string, username: string) {
  return request<{ status: string }>(`/admin/users/${username}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Notifications ──────────────────────────────────────────
export function listNotifications(csrf: string) {
  return request<import("./types").Notification[]>("/admin/notifications", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function createNotification(csrf: string, body: { title: string; content: string; is_important?: boolean }) {
  return request<import("./types").Notification>("/admin/notifications", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function updateNotification(csrf: string, id: number, body: { title?: string; content?: string; is_important?: boolean; is_active?: boolean }) {
  return request<import("./types").Notification>(`/admin/notifications/${id}`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function deleteNotification(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/notifications/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Settings ───────────────────────────────────────────────
export function getSettings(csrf: string) {
  return request<Record<string, any>>("/admin/settings", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function updateSettings(csrf: string, settings: Record<string, any>) {
  return request<{ status: string }>("/admin/settings", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(settings),
  });
}

// ── Admin: Homepage ───────────────────────────────────────────────
export function getAdminHome(csrf: string) {
  return request<{
    subsite_links: import("./types").SubsiteLink[];
    featured_articles: import("./types").FeaturedArticle[];
  }>("/admin/home", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function addSubsiteLink(csrf: string, body: { name: string; url: string; description?: string; sort_order?: number }) {
  return request<{ status: string }>("/admin/home/links", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function deleteSubsiteLink(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/home/links/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function addFeatured(csrf: string, articleId: number) {
  return request<{ status: string }>("/admin/home/featured", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ article_id: articleId }),
  });
}

export function deleteFeatured(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/home/featured/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Profile ────────────────────────────────────────────────
export function getProfile(csrf: string) {
  return request<import("./types").User>("/admin/profile", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function updateProfile(csrf: string, body: { nickname?: string; bio?: string; avatar?: string }) {
  return request<import("./types").User>("/admin/profile", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}
