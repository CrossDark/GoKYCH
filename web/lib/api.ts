const BASE =
  typeof window === "undefined"
    ? process.env.API_BASE_URL || "http://localhost:8000"
    : "";

async function getServerCookies(): Promise<string> {
  if (typeof window !== "undefined") return "";
  try {
    const { cookies } = await import("next/headers");
    const jar = await cookies();
    return jar.getAll().map((c) => `${c.name}=${c.value}`).join("; ");
  } catch {
    return "";
  }
}

async function request<T>(
  path: string,
  options?: RequestInit
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options?.headers as Record<string, string>),
  };

  // Forward session cookie on server-side so Go API can identify the user
  if (typeof window === "undefined") {
    const cookieStr = await getServerCookies();
    if (cookieStr) headers["Cookie"] = cookieStr;
  }

  const res = await fetch(`${BASE}/api${path}`, {
    ...(typeof window !== "undefined" ? { credentials: "include" } : {}),
    ...options,
    headers,
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
// authorId: when set, server filters to articles authored by this user (used
// by the "我的文章" view for regular users on /admin/articles).
export function listArticles(
  type?: string,
  page = 1,
  authorId?: number
) {
  const q = new URLSearchParams();
  if (type) q.set("type", type);
  if (authorId) q.set("author_id", String(authorId));
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

// ── Line Comments ─────────────────────────────────────────────────
export function getLineComments(type: string, slug: string) {
  return request<{ counts: Record<number, number>; comments: (import("./types").Comment & { line_number: number })[] }>(
    `/articles/${type}/${slug}/line-comments`
  );
}

export function addLineComment(
  type: string, slug: string, csrf: string,
  body: { line_number: number; content: string; author_name?: string }
) {
  return request<import("./types").Comment>(
    `/articles/${type}/${slug}/line-comments`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify(body),
    }
  );
}

// ── Rating ────────────────────────────────────────────────────────
export function getRating(type: string, slug: string) {
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`
  );
}

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

export function undoRating(type: string, slug: string, csrf: string) {
  return request<import("./types").RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

export function getRatingDetails(type: string, slug: string) {
  return request<{ ratings: import("./types").RatingDetail[] }>(
    `/articles/${type}/${slug}/ratings`
  );
}

// ── Home ──────────────────────────────────────────────────────────
export function getHome() {
  return request<import("./types").HomeData>("/home");
}

// ── Site config ──────────────────────────────────────────────────
//
// One-shot read of title/subtitle/theme/ICP/subsite_links, used by the
// global Header and LayoutWrapper footer. Public endpoint, no auth.
export function getSite() {
  return request<import("./types").SiteConfig>("/site");
}

// Public theme list — names + meta + has_css. Used by the admin settings
// page to populate the theme dropdown. Theme CSS itself is fetched
// directly from /api/themes/:name.css (text/css, no JSON wrapping).
export function listThemes() {
  return request<import("./types").Theme[]>("/themes");
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

// Self-service password change. Any logged-in user can change their own
// password; the handler verifies the old one before accepting the new.
export function changeMyPassword(csrf: string, body: { old_password: string; new_password: string }) {
  return request<{ status: string; message?: string }>("/admin/profile/password", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

// ── Admin: Tags (full CRUD) ─────────────────────────────────────────
// Used by /admin/tags — the public /api/labels endpoint only returns tags
// that have at least one article, so admins need a separate route to manage
// empty tags too.
export function listAdminTags(csrf: string) {
  return request<import("./types").AdminTag[]>("/admin/tags", {
    headers: { "X-CSRF-Token": csrf },
  });
}

export function createTag(csrf: string, name: string) {
  return request<{ id: number; status: string; existed?: boolean }>("/admin/tags", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ name }),
  });
}

export function renameTag(csrf: string, id: number, name: string) {
  return request<{ status: string }>(`/admin/tags/${id}`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ name }),
  });
}

export function deleteAdminTag(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/tags/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Admin: Files (upload/delete) ────────────────────────────────────
// listAdminFiles is already declared above; uploadFile/deleteFile cover the
// write paths.
export function uploadFile(csrf: string, file: File) {
  const fd = new FormData();
  fd.append("file", file);
  // When sending FormData the browser sets its own multipart Content-Type
  // with boundary — passing "Content-Type: application/json" from `request`
  // would break the upload, so we build the fetch manually.
  const headers: Record<string, string> = { "X-CSRF-Token": csrf };
  if (typeof window === "undefined") {
    // Server-side upload isn't expected in this UI, but keep the path working
    // if it ever is.
    return import("next/headers").then(async ({ cookies }) => {
      const jar = await cookies();
      const cookieStr = jar.getAll().map((c) => `${c.name}=${c.value}`).join("; ");
      headers["Cookie"] = cookieStr;
      const res = await fetch(`${process.env.API_BASE_URL || "http://localhost:8000"}/api/admin/files`, {
        method: "POST",
        headers,
        body: fd,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ApiError(res.status, body.error || "上传失败");
      }
      return res.json() as Promise<{ status: string; filename: string; url: string; deduped?: boolean }>;
    });
  }
  return fetch(`/api/admin/files`, {
    method: "POST",
    headers,
    body: fd,
    credentials: "include",
  }).then(async (res) => {
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new ApiError(res.status, body.error || "上传失败");
    }
    return res.json() as Promise<{ status: string; filename: string; url: string; deduped?: boolean }>;
  });
}

export function deleteAdminFile(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/files/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function listAdminFiles(csrf: string) {
  return request<import("./types").AdminFile[]>("/admin/files", {
    headers: { "X-CSRF-Token": csrf },
  });
}
