import { request } from "./client";
import JSZip from "jszip";
import type {
  User,
  Notification,
  SubsiteLink,
  FeaturedArticle,
  AdminTag,
  AdminFile,
  SiteSettings,
  ApiKey,
  AdminSidebarCard,
  CreateApiKeyResponse,
  PasskeyInfo,
  MyPasskeyInfo,
  UpdateCheckInfo,
  UpdateStatus,
  Theme,
  SettingDefinition,
} from "@/lib/types";

export function listUsers(csrf: string) {
  return request<User[]>("/admin/users", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createUser(csrf: string, body: { username: string; password: string; nickname?: string; role?: string }) {
  return request<User>("/admin/users", {
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

export function listNotifications(csrf: string) {
  return request<Notification[]>("/admin/notifications", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createNotification(csrf: string, body: { title: string; content: string; is_important?: boolean }) {
  return request<Notification>("/admin/notifications", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function updateNotification(csrf: string, id: number, body: { title?: string; content?: string; is_important?: boolean; is_active?: boolean }) {
  return request<Notification>(`/admin/notifications/${id}`, {
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

export function getSettings(csrf: string) {
  return request<SiteSettings>("/admin/settings", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function updateSettings(csrf: string, settings: SiteSettings) {
  return request<{ status: string }>("/admin/settings", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(settings),
  });
}

export function getAdminHome(csrf: string) {
  return request<{
    subsite_links: SubsiteLink[];
    featured_articles: FeaturedArticle[];
  }>("/admin/home", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
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

export function getProfile(csrf: string) {
  return request<User>("/admin/profile", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function updateProfile(csrf: string, body: {
  nickname?: string;
  bio?: string;
  avatar?: string;
  social_email?: string;
  social_github?: string;
  social_qq?: string;
}) {
  return request<User>("/admin/profile", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function changeMyPassword(csrf: string, body: { old_password: string; new_password: string }) {
  return request<{ status: string; message?: string }>("/admin/profile/password", {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function listAdminTags(csrf: string) {
  return request<AdminTag[]>("/admin/tags", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
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

// ── Sidebar cards admin CRUD ────────────────────────────────────────
// All admin writes invalidate the [\"home\", \"sidebar_cards\"] frontend
// cache tags server-side (see revalidateFrontend in admin.go), so the
// `request(...)` calls here don't have to issue per-write cache
// invalidations themselves; the next page fetch picks up the change
// within seconds.

export function listAdminSidebarCards(csrf: string) {
  return request<AdminSidebarCard[]>("/admin/sidebar-cards", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createSidebarCard(csrf: string, body: AdminSidebarCard) {
  // Stamp out the fields we don't want to round-trip from the
  // admin form (id / created_at / updated_at). The backend accepts
  // is_active/is_external as *bool with default-true semantics for
  // omission, so a stale admin form that omits them still produces
  // a sensible row.
  const { id: _id, ...payload } = body;
  return request<{ id: number; status: string }>("/admin/sidebar-cards", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(payload),
  });
}

export function updateSidebarCard(csrf: string, id: number, body: AdminSidebarCard) {
  const { id: _id, ...payload } = body;
  return request<{ status: string }>(`/admin/sidebar-cards/${id}`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(payload),
  });
}

export function deleteAdminSidebarCard(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/sidebar-cards/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function uploadFile(csrf: string, file: File) {
  const fd = new FormData();
  fd.append("file", file);
  return request<{ status: string; filename: string; url: string; deduped?: boolean }>("/admin/files", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: fd,
  });
}

export function deleteAdminFile(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/files/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function listAdminFiles(csrf: string) {
  return request<AdminFile[]>("/admin/files", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function listApiKeys(csrf: string) {
  return request<ApiKey[]>("/admin/api-keys", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function createApiKey(csrf: string, name: string) {
  return request<CreateApiKeyResponse>("/admin/api-keys", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ name }),
  });
}

export function deleteApiKey(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/api-keys/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function listAllPasskeys(csrf: string) {
  return request<PasskeyInfo[]>("/admin/passkeys", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function deleteAnyPasskey(csrf: string, id: number) {
  return request<{ status: string }>(`/admin/passkeys/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function listMyPasskeys(csrf: string) {
  return request<MyPasskeyInfo[]>("/auth/passkey", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export interface PasskeyCredential {
  id: string;
  type: string;
  rawId: string;
  response: {
    clientDataJSON: string;
    attestationObject: string;
  };
}

export interface PasskeyPublicKeyCredentialDescriptor {
  id: string;
  type: string;
  transports?: string[];
}

export interface PasskeyUserEntity {
  id: string;
  name: string;
  displayName: string;
}

export interface PasskeyRegistrationOptions {
  challenge: string;
  rp: { name: string; id?: string };
  user: PasskeyUserEntity;
  pubKeyCredParams: Array<{ type: string; alg: number }>;
  timeout?: number;
  excludeCredentials?: PasskeyPublicKeyCredentialDescriptor[];
  authenticatorSelection?: {
    authenticatorAttachment?: string;
    residentKey?: string;
    requireResidentKey?: boolean;
    userVerification?: string;
  };
  attestation?: string;
  extensions?: Record<string, unknown>;
}

export function beginPasskeyRegister(csrf: string) {
  return request<{ publicKey: PasskeyRegistrationOptions }>("/auth/passkey/register/begin", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function finishPasskeyRegister(csrf: string, body: { name: string; credential: PasskeyCredential }) {
  return request<{ status: string }>("/auth/passkey/register/finish", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
}

export function deleteMyPasskey(csrf: string, id: number) {
  return request<{ status: string }>(`/auth/passkey/${id}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function getUpdateStatus() {
  return request<UpdateStatus>("/admin/update/status", {
    next: { revalidate: 0 },
  });
}

export function checkUpdate() {
  return request<UpdateCheckInfo>("/admin/update/check", {
    next: { revalidate: 0 },
  });
}

export interface ApplyUpdateResult {
  success: boolean;
  message: string;
  error?: string;
}

export function applyUpdate(csrf: string) {
  return request<ApplyUpdateResult>("/admin/update/apply", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({}),
  });
}

export interface SetUpdateSourceResult {
  status: string;
  source: "github" | "gitcode";
  message: string;
}

// Switch the persisted update.source setting ("github" ↔ "gitcode").
// Next /update/check will use the new source. Cache on the server is
// invalidated as part of the request.
export function setUpdateSource(csrf: string, source: "github" | "gitcode") {
  return request<SetUpdateSourceResult>("/admin/update/source", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: JSON.stringify({ source }),
  });
}

// ── Theme management (owner-only) ────────────────────────────────────

export function adminListThemes(csrf: string) {
  return request<Theme[]>("/admin/themes", {
    headers: { "X-CSRF-Token": csrf },
    next: { revalidate: 0 },
  });
}

export function uploadThemeZip(csrf: string, file: File) {
  const fd = new FormData();
  fd.append("zip", file);
  return request<Theme>("/admin/themes/upload", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: fd,
  });
}

export function uploadThemeCSS(csrf: string, file: File, displayName?: string) {
  const fd = new FormData();
  fd.append("css", file);
  if (displayName) fd.append("name", displayName);
  return request<Theme>("/admin/themes/upload", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: fd,
  });
}

export async function uploadThemeFolder(csrf: string, files: FileList): Promise<Theme> {
  const zip = new JSZip();
  let commonPrefix = "";
  const paths: string[] = [];
  for (let i = 0; i < files.length; i++) {
    const file = files.item(i)!;
    paths.push(file.webkitRelativePath || file.name);
  }
  if (paths.length > 0) {
    const firstPath = paths[0];
    const firstSlash = firstPath.indexOf("/");
    if (firstSlash !== -1) {
      const candidate = firstPath.substring(0, firstSlash + 1);
      const allMatch = paths.every((p) => p.startsWith(candidate));
      if (allMatch) {
        commonPrefix = candidate;
      }
    }
  }
  for (let i = 0; i < files.length; i++) {
    const file = files.item(i)!;
    let path = file.webkitRelativePath || file.name;
    if (commonPrefix && path.startsWith(commonPrefix)) {
      path = path.substring(commonPrefix.length);
    }
    await zip.file(path, file);
  }
  const blob = await zip.generateAsync({ type: "blob", compression: "DEFLATE" });
  const zipFile = new File([blob], "theme.zip", { type: "application/zip" });
  const fd = new FormData();
  fd.append("zip", zipFile);
  return request<Theme>("/admin/themes/upload", {
    method: "POST",
    headers: { "X-CSRF-Token": csrf },
    body: fd,
  });
}

export function deleteTheme(csrf: string, name: string) {
  return request<{ status: string }>(`/admin/themes/${name}`, {
    method: "DELETE",
    headers: { "X-CSRF-Token": csrf },
  });
}

export function activateTheme(csrf: string, name: string) {
  return request<{ status: string; active: string }>(`/admin/themes/${name}/activate`, {
    method: "PUT",
    headers: { "X-CSRF-Token": csrf },
  });
}

// ── Theme settings (P10) ─────────────────────────────────────────────
//
// Public read of schema + admin overrides at GET /api/themes/:name/settings.
// Used by the runtime glass-fx layer to pick up the EFFECTIVE mode
// without going through localStorage. The admin modal also uses this to
// initialise the form (so the editor sees the current state, not the
// schema defaults).
export interface ThemeSettingsResponse {
  schema: SettingDefinition[];
  values: Record<string, string>;
}

export function getThemeSettings(name: string) {
  return request<ThemeSettingsResponse>(`/themes/${name}/settings`, {
    next: { revalidate: 0 },
  });
}

// Owner-only write of admin overrides. PUT body is { values: { key: val } }.
// Server validates every value against the schema (select: must be in
// options; range: must be int within min/max) and returns 400 with a
// `rejects` list if anything's off.
export function updateThemeSettings(
  csrf: string,
  name: string,
  values: Record<string, string>
) {
  return request<{ status: string; updated: number }>(
    `/admin/themes/${name}/settings`,
    {
      method: "PUT",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify({ values }),
    }
  );
}