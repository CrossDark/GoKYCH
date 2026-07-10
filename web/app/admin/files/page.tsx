"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  deleteAdminFile,
  getCsrf,
  getMe,
  getSettings,
  getSite,
  listAdminFiles,
  updateSettings,
  uploadFile,
} from "@/lib/api";
import type { AdminFile, SiteConfig, SiteSettings, User } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { fmtDateTime } from "@/lib/format";

const IMAGE_EXTS = /\.(png|jpe?g|gif|webp|svg|ico|bmp)$/i;
const ACCEPT_HINT = "png / jpg / gif / webp / svg / ico / pdf / md / txt / css";

export default function AdminFiles() {
  const [csrf, setCsrf] = useState("");
  const [me, setMe] = useState<User | null>(null);
  const [siteConfig, setSiteConfig] = useState<SiteConfig | null>(null);
  const [settings, setSettings] = useState<SiteSettings | null>(null);
  const [files, setFiles] = useState<AdminFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [uploading, setUploading] = useState(false);
  const [togglingUserFiles, setTogglingUserFiles] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<AdminFile | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const toast = useToast();

  const isOwner = me?.role === "owner";
  const isAdminLike = me?.role === "admin" || me?.role === "owner";
  const allowPersonalFiles = siteConfig?.features?.allow_user_file_management === true;
  const canUpload = isAdminLike || allowPersonalFiles;
  const pageTitle = isAdminLike ? "文件管理" : "我的文件";
  const pageSubtitle = isAdminLike
    ? "上传、引用、清理站点静态资源"
    : "上传并管理你自己的文件";

  const totalSize = useMemo(() => files.reduce((sum, f) => sum + (f.file_size || 0), 0), [files]);
  const imageCount = useMemo(() => files.filter((f) => isImageFile(f)).length, [files]);
  const pdfCount = useMemo(() => files.filter((f) => f.mime_type === "application/pdf" || f.filename.toLowerCase().endsWith(".pdf")).length, [files]);

  const load = async (token = csrf) => {
    if (!token) return;
    setLoading(true);
    setError("");
    try {
      const nextFiles = await listAdminFiles(token);
      setFiles(nextFiles);
    } catch (err: any) {
      setFiles([]);
      setError(err.message || "加载文件列表失败。");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [csrfRes, meRes, siteRes] = await Promise.all([
          getCsrf(),
          getMe().catch(() => ({ user: null })),
          getSite().catch(() => null),
        ]);
        if (cancelled) return;
        setCsrf(csrfRes.csrf_token);
        setMe(meRes.user ?? null);
        if (siteRes) setSiteConfig(siteRes);
        if (meRes.user?.role === "owner") {
          getSettings(csrfRes.csrf_token).then((cfg) => {
            if (!cancelled) setSettings(cfg);
          }).catch(() => {});
        }
        void load(csrfRes.csrf_token);
      } catch (err: any) {
        if (!cancelled) {
          setError(err.message || "初始化文件管理失败。");
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // Initial load only; subsequent refreshes are triggered explicitly after upload/delete.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openUploadPicker = () => {
    if (!canUpload) {
      toast.warning("站长尚未开放普通用户文件上传。");
      return;
    }
    inputRef.current?.click();
  };

  const doUpload = async (picked: FileList | null) => {
    if (!picked || picked.length === 0) return;
    if (!csrf) return;
    if (!canUpload) {
      toast.warning("站长尚未开放普通用户文件上传。");
      return;
    }
    setUploading(true);
    let ok = 0;
    let fail = 0;
    let dedup = 0;
    const failures: string[] = [];
    try {
      for (const f of Array.from(picked)) {
        try {
          const r = await uploadFile(csrf, f);
          if (r.deduped) dedup++;
          else ok++;
        } catch (err: any) {
          fail++;
          failures.push(f.name);
          // Continue with the rest — one bad file shouldn't block the batch.
          console.error("upload failed:", f.name, err);
        }
      }
      if (ok > 0 || dedup > 0) {
        toast.success(`上传完成：${ok} 个新增，${dedup} 个去重${fail > 0 ? `，${fail} 个失败` : ""}。`);
      }
      if (fail > 0) {
        toast.error(`${fail} 个文件上传失败：${failures.slice(0, 3).join("、")}${failures.length > 3 ? "…" : ""}`);
      }
      await load(csrf);
    } finally {
      setUploading(false);
      if (inputRef.current) inputRef.current.value = "";
    }
  };

  const handleSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    void doUpload(e.target.files);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    if (!canUpload) return;
    void doUpload(e.dataTransfer.files);
  };

  const copyUrl = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      toast.success(`已复制链接：${url}`);
    } catch {
      toast.warning(`请手动复制：${url}`);
    }
  };

  const handleDelete = (f: AdminFile) => setPendingDelete(f);
  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const f = pendingDelete;
    try {
      await deleteAdminFile(csrf, f.id);
      toast.success(`已删除「${f.original_name || f.filename}」。`);
      await load(csrf);
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err;
    } finally {
      setPendingDelete(null);
    }
  };

  const toggleUserFileManagement = async (enabled: boolean) => {
    if (!settings) return;
    const next: SiteSettings = {
      ...settings,
      features: {
        ...settings.features,
        allow_user_file_management: enabled,
      },
    };
    setTogglingUserFiles(true);
    try {
      await updateSettings(csrf, next);
      setSettings(next);
      setSiteConfig((prev) => prev ? {
        ...prev,
        features: {
          ...prev.features,
          allow_user_file_management: enabled,
        },
      } : prev);
      toast.success(enabled ? "已开放普通用户管理自己的文件。" : "已关闭普通用户文件管理。");
    } catch (err: any) {
      toast.error(err.message || "保存开关失败。");
    } finally {
      setTogglingUserFiles(false);
    }
  };

  return (
    <div className="admin-files">
      <input
        ref={inputRef}
        type="file"
        multiple
        className="admin-file-hidden-input"
        onChange={handleSelect}
        disabled={uploading || !canUpload}
      />

      <div className="admin-page-header admin-files-header">
        <div>
          <h1>{pageTitle}</h1>
          <div className="admin-page-subtitle">{pageSubtitle}</div>
        </div>
        <div className="admin-header-actions">
          <button
            type="button"
            className={`admin-btn admin-btn-primary ${uploading ? "admin-btn-loading" : ""}`}
            onClick={openUploadPicker}
            disabled={uploading || !canUpload}
          >
            ＋ 上传文件
          </button>
          <button type="button" className="admin-btn admin-btn-ghost" onClick={() => void load()} disabled={loading}>
            刷新
          </button>
        </div>
      </div>

      <section className="admin-files-hero">
        <div className="admin-files-hero-main">
          <div className="admin-files-kicker">{isAdminLike ? "全站文件库" : "个人文件库"}</div>
          <h2>{files.length > 0 ? `当前共有 ${files.length} 个文件` : "把文件拖进来，后台会自动生成可引用链接"}</h2>
          <p>
            支持 {ACCEPT_HINT}，单文件最大 10 MB。上传后可直接复制链接用于文章、Logo、Favicon 或主题资源。
          </p>
          <div className="admin-files-hero-actions">
            <button
              type="button"
              className="admin-btn admin-btn-secondary"
              onClick={openUploadPicker}
              disabled={uploading || !canUpload}
            >
              选择文件
            </button>
            <span>{canUpload ? "也可以拖拽到下方上传区" : "站长开启后普通用户才可上传"}</span>
          </div>
        </div>
        <div className="admin-files-stat-grid" aria-label="文件概览">
          <StatCard label="文件数" value={loading ? "—" : String(files.length)} />
          <StatCard label="总大小" value={loading ? "—" : fmtSize(totalSize)} />
          <StatCard label="图片" value={loading ? "—" : String(imageCount)} />
          <StatCard label="PDF" value={loading ? "—" : String(pdfCount)} />
        </div>
      </section>

      {isOwner && (
        <section className="admin-card admin-files-permission-card">
          <div className="admin-card-body">
            <div className="admin-files-permission-copy">
              <div className="admin-files-permission-title">允许所有用户上传和删除自己的文件</div>
              <div className="admin-files-permission-desc">
                开启后普通用户会在后台看到「我的文件」，只能查看、上传、删除自己上传的文件；管理员和站长仍管理全站文件。
              </div>
            </div>
            <label className="admin-switch" aria-label="允许普通用户管理自己的文件">
              <input
                type="checkbox"
                checked={settings?.features?.allow_user_file_management ?? allowPersonalFiles}
                disabled={!settings || togglingUserFiles}
                onChange={(e) => void toggleUserFileManagement(e.target.checked)}
              />
              <span className="admin-switch-track" />
            </label>
          </div>
        </section>
      )}

      {!canUpload && (
        <div className="admin-notice admin-notice-warning">
          <span className="admin-notice-icon">⚠</span>
          <div className="admin-notice-content">当前没有文件上传权限。需要站长开启「允许所有用户上传和删除自己的文件」后才能使用。</div>
        </div>
      )}

      <section className="admin-card admin-upload-card">
        <div className="admin-card-header">
          <h2>上传入口</h2>
          <div className="admin-card-actions">
            <button
              type="button"
              className="admin-btn admin-btn-outline admin-btn-sm"
              onClick={openUploadPicker}
              disabled={uploading || !canUpload}
            >
              打开文件选择器
            </button>
          </div>
        </div>
        <div className="admin-card-body">
          <div
            className={`admin-upload-dropzone ${dragOver ? "drag-over" : ""} ${!canUpload ? "disabled" : ""}`}
            onDragOver={(e) => {
              e.preventDefault();
              if (canUpload) setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
            onClick={openUploadPicker}
          >
            <div className="admin-upload-icon">📤</div>
            <div className="admin-upload-prompt">
              {uploading ? (
                <p>
                  <span className="admin-spinner" style={{ marginRight: 6, verticalAlign: "middle" }} />
                  上传中…
                </p>
              ) : (
                <>
                  <p className="admin-upload-title">点击选择文件，或拖拽到这里</p>
                  <p className="admin-upload-hint">{ACCEPT_HINT} · 单文件最大 10 MB</p>
                </>
              )}
            </div>
          </div>
        </div>
      </section>

      {error && (
        <div className="admin-notice admin-notice-error">
          <span className="admin-notice-icon">!</span>
          <div className="admin-notice-content">{error}</div>
        </div>
      )}

      <section className="admin-card">
        <div className="admin-card-header">
          <h2>{isAdminLike ? "文件列表" : "我的文件"}</h2>
          <span className="admin-card-muted">{loading ? "加载中…" : `${files.length} 个文件`}</span>
        </div>
        <div className="admin-card-body">
          {loading ? (
            <div className="admin-empty">加载中…</div>
          ) : files.length === 0 ? (
            <div className="admin-empty admin-files-empty">
              <span className="admin-empty-icon">📁</span>
              <div className="admin-empty-title">还没有上传文件</div>
              <div>{canUpload ? "点上面的上传按钮，或者把文件拖进上传区。" : "当前账号暂时没有文件上传权限。"}</div>
            </div>
          ) : (
            <div className="admin-file-grid">
              {files.map((f) => {
                const url = f.url || `/uploads/${f.filename}`;
                const isImage = isImageFile(f);
                const uploader = f.uploader_nickname || f.uploader_name || (f.uploaded_by ? `#${f.uploaded_by}` : "—");
                return (
                  <article key={f.id} className="admin-file-card">
                    <div className="admin-file-preview">
                      {isImage ? (
                        <img src={url} alt={f.original_name || f.filename} loading="lazy" />
                      ) : (
                        <span className="admin-file-card-icon">{fileIcon(f)}</span>
                      )}
                    </div>
                    <div className="admin-file-card-body">
                      <div className="admin-file-card-title" title={f.original_name || f.filename}>{f.original_name || f.filename}</div>
                      <div className="admin-file-card-meta">
                        <span>{fmtSize(f.file_size)}</span>
                        <span>{shortMime(f.mime_type)}</span>
                      </div>
                      <div className="admin-file-card-stored" title={f.filename}>{f.filename}</div>
                      <div className="admin-file-card-footer">
                        <span>{fmtDateTime(f.created_at)}</span>
                        {isAdminLike && <span>上传者：{uploader}</span>}
                      </div>
                    </div>
                    <div className="admin-file-card-actions">
                      <button className="admin-btn admin-btn-outline admin-btn-sm" onClick={() => copyUrl(url)} title="复制链接">复制</button>
                      <a
                        className="admin-btn admin-btn-secondary admin-btn-sm"
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        title="打开"
                      >打开</a>
                      <button
                        className="admin-btn admin-btn-danger admin-btn-sm"
                        onClick={() => handleDelete(f)}
                        title="删除"
                      >删除</button>
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </div>
      </section>

      <AdminConfirm
        open={!!pendingDelete}
        title="删除文件"
        message={pendingDelete ? `确定要删除「${pendingDelete.original_name || pendingDelete.filename}」吗？此操作不可撤销。` : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="admin-files-stat-card">
      <div className="admin-files-stat-value">{value}</div>
      <div className="admin-files-stat-label">{label}</div>
    </div>
  );
}

function isImageFile(f: AdminFile) {
  return IMAGE_EXTS.test(f.filename) || (f.mime_type?.startsWith("image/") ?? false);
}

function fileIcon(f: AdminFile) {
  const mime = f.mime_type || "";
  const name = f.filename.toLowerCase();
  if (mime === "application/pdf" || name.endsWith(".pdf")) return "📕";
  if (mime.includes("markdown") || name.endsWith(".md")) return "📝";
  if (mime.includes("css") || name.endsWith(".css")) return "🎨";
  if (mime.startsWith("text/") || name.endsWith(".txt")) return "📄";
  return "📦";
}

function shortMime(mime: string) {
  if (!mime) return "未知类型";
  if (mime.length <= 22) return mime;
  return mime.replace("application/", "app/").replace("image/", "img/");
}

function fmtSize(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}
