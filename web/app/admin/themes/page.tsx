"use client";

import { useEffect, useRef, useState } from "react";
import {
  getCsrf,
  getSite,
  adminListThemes,
  uploadThemeZip,
  uploadThemeCSS,
  uploadThemeFolder,
  deleteTheme,
  activateTheme,
} from "@/lib/api";
import { apiUrl } from "@/lib/api/client";
import type { Theme } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { ThemeSettingsModal } from "@/components/admin/ThemeSettingsModal";

const THEME_PREVIEW_HTML = `
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: system-ui, sans-serif; padding: 16px; }
  .demo-card { border-radius: 8px; padding: 12px; margin-bottom: 10px; }
  .demo-title { font-size: 15px; font-weight: 700; margin-bottom: 6px; }
  .demo-text { font-size: 12px; line-height: 1.5; margin-bottom: 6px; }
  .demo-btn { display: inline-block; padding: 4px 12px; border-radius: 4px; font-size: 11px; cursor: pointer; border: none; }
  .demo-btn-primary { margin-right: 6px; }
  .demo-tag { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 10px; margin-right: 4px; }
  .demo-link { font-size: 11px; text-decoration: underline; cursor: pointer; }
  .demo-meta { font-size: 10px; opacity: 0.7; margin-top: 6px; }
</style>
</head>
<body>
  <div class="demo-card">
    <div class="demo-title">跨越晨昏 · 示例标题</div>
    <div class="demo-text">这是一段示例正文，用于展示主题的排版配色效果。The quick brown fox jumps over the lazy dog.</div>
    <div>
      <span class="demo-tag demo-tag-primary">标签一</span>
      <span class="demo-tag">标签二</span>
    </div>
    <div class="demo-meta">2024-01-15 · 阅读 328</div>
  </div>
  <div class="demo-card" style="text-align:center;">
    <button class="demo-btn demo-btn-primary">主要按钮</button>
    <button class="demo-btn">次要按钮</button>
  </div>
</body>
</html>
`;

function fmtDate(s?: string): string {
  if (!s) return "—";
  try {
    return new Date(s).toLocaleString("zh-CN", {
      year: "numeric", month: "2-digit", day: "2-digit",
      hour: "2-digit", minute: "2-digit",
    });
  } catch { return s; }
}

function themePreviewUrl(name: string, updatedAt?: string): string {
  let url = apiUrl(`/api/themes/${name}`);
  if (updatedAt) url += `?v=${new Date(updatedAt).getTime()}`;
  return url;
}

export default function AdminThemes() {
  const [csrf, setCsrf] = useState("");
  const [themes, setThemes] = useState<Theme[]>([]);
  const [activeName, setActiveName] = useState<string>("sunset");
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<Theme | null>(null);
  const [cssName, setCssName] = useState("");
  const [settingsTarget, setSettingsTarget] = useState<Theme | null>(null);
  const toast = useToast();
  const uploadRef = useRef<HTMLInputElement | null>(null);

  const load = () => {
    if (!csrf) return;
    setLoading(true);
    Promise.all([
      adminListThemes(csrf).catch(() => [] as Theme[]),
      getSite().catch(() => null),
    ]).then(([list, site]) => {
      setThemes(list);
      const cur = (site?.appearance as any)?.style_theme;
      if (cur) setActiveName(cur);
    }).finally(() => setLoading(false));
  };

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token));
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (csrf) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [csrf]);

  const doUpload = async (files: FileList) => {
    if (!files || files.length === 0) return;
    setUploading(true);
    try {
      const firstFile = files.item(0)!;
      if (files.length === 1 && firstFile.name.endsWith(".zip")) {
        const installed = await uploadThemeZip(csrf, firstFile);
        toast.success(`主题「${installed.name}」安装成功！`);
      } else if (files.length === 1 && firstFile.name.endsWith(".css")) {
        const installed = await uploadThemeCSS(csrf, firstFile, cssName || undefined);
        toast.success(`主题「${installed.name}」安装成功！`);
        setCssName("");
      } else {
        const installed = await uploadThemeFolder(csrf, files);
        toast.success(`主题「${installed.name}」安装成功！`);
      }
      load();
    } catch (err: any) {
      toast.error(err.message || "主题安装失败。");
    } finally {
      setUploading(false);
      if (uploadRef.current) uploadRef.current.value = "";
    }
  };

  const handleActivate = async (name: string) => {
    try {
      const r = await activateTheme(csrf, name);
      setActiveName(r.active);
      toast.success(`已切换到主题「${r.active}」，刷新页面即可生效。`);
    } catch (err: any) {
      toast.error(err.message || "切换主题失败。");
    }
  };

  const handleDelete = (t: Theme) => setPendingDelete(t);
  const confirmDelete = async () => {
    if (!pendingDelete) return;
    const t = pendingDelete;
    try {
      await deleteTheme(csrf, t.name);
      toast.success(`已删除主题「${t.name}」。`);
      load();
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err;
    } finally {
      setPendingDelete(null);
    }
  };

  const handleUploadSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (files) doUpload(files);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const files = e.dataTransfer.files;
    if (!files || files.length === 0) return;
    doUpload(files);
  };

  const builtins = themes.filter((t) => t.builtin);
  const customs = themes.filter((t) => !t.builtin);

  return (
    <div className="admin-themes">
      <div className="admin-page-header">
        <div>
          <h1>🎨 主题管理</h1>
          <div className="admin-page-subtitle">
            内置 {builtins.length} 个主题 · 用户主题 {customs.length} 个
            {activeName && <> · 当前激活：<strong>{activeName}</strong></>}
          </div>
        </div>
      </div>

      {/* Upload zone */}
      <div className="admin-card">
        <div className="admin-card-header">
          <h2>⬆ 上传主题</h2>
        </div>
        <div className="admin-card-body">
          <div
            className={`admin-upload-dropzone ${dragOver ? "drag-over" : ""}`}
            onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
          >
            {uploading ? (
              <div className="admin-upload-prompt">
                <p>
                  <span className="admin-spinner" style={{ marginRight: 6, verticalAlign: "middle" }} />
                  上传中…
                </p>
              </div>
            ) : (
              <div className="admin-upload-prompt">
                <p className="admin-upload-title">拖拽主题文件夹、.zip 或 .css 文件到此处，或点击下方按钮选择</p>
                <p className="admin-upload-hint">
                  文件夹或 ZIP 包需包含 theme.yaml 和 static/theme.css；CSS 文件可直接上传（自动生成元数据）
                </p>
              </div>
            )}
          </div>

          <div style={{ display: "flex", gap: 12, marginTop: 16, flexWrap: "wrap", alignItems: "center" }}>
            <input
              ref={uploadRef}
              type="file"
              accept=".zip,.css"
              style={{ display: "none" }}
              onChange={handleUploadSelect}
              disabled={uploading}
              // @ts-ignore - webkitdirectory is a non-standard WebKit property for folder uploads
              webkitdirectory="true"
            />
            <button
              className="admin-btn admin-btn-primary"
              onClick={() => uploadRef.current?.click()}
              disabled={uploading}
            >
              📦 上传主题（文件夹 / ZIP / CSS）
            </button>

            <input
              type="text"
              placeholder="主题名称（上传 CSS 时使用）"
              value={cssName}
              onChange={(e) => setCssName(e.target.value)}
              style={{
                padding: "6px 10px",
                border: "1px solid var(--border)",
                borderRadius: 4,
                fontSize: "0.85rem",
                width: 160,
                background: "var(--bg-input, var(--surface))",
                color: "var(--text)",
              }}
              disabled={uploading}
            />
          </div>
        </div>
      </div>

      {/* Built-in themes */}
      {loading ? (
        <div className="admin-card"><div className="admin-card-body"><div className="admin-empty">加载中…</div></div></div>
      ) : (
        <>
          <div className="admin-card">
            <div className="admin-card-header">
              <h2>📦 内置主题（{builtins.length}）</h2>
              <span style={{ fontSize: "0.8rem", color: "var(--text-muted)" }}>随系统内置，不可删除</span>
            </div>
            <div className="admin-card-body no-padding">
              <div className="theme-grid">
                {builtins.map((t) => (
                  <ThemeCard
                    key={t.name}
                    theme={t}
                    active={t.name === activeName}
                    onActivate={() => handleActivate(t.name)}
                    onSettings={() => setSettingsTarget(t)}
                  />
                ))}
              </div>
            </div>
          </div>

          {/* User themes */}
          <div className="admin-card">
            <div className="admin-card-header">
              <h2>🎨 我的主题（{customs.length}）</h2>
              <span style={{ fontSize: "0.8rem", color: "var(--text-muted)" }}>用户上传的主题</span>
            </div>
            <div className="admin-card-body no-padding">
              {customs.length === 0 ? (
                <div className="admin-empty" style={{ padding: "40px 20px" }}>
                  <span className="admin-empty-icon">🎨</span>
                  <div className="admin-empty-title">还没有上传自定义主题</div>
                  <div>通过上方上传区域添加你的主题</div>
                </div>
              ) : (
                <div className="theme-grid">
                  {customs.map((t) => (
                    <ThemeCard
                      key={t.name}
                      theme={t}
                      active={t.name === activeName}
                      onActivate={() => handleActivate(t.name)}
                      onDelete={() => handleDelete(t)}
                      onSettings={() => setSettingsTarget(t)}
                    />
                  ))}
                </div>
              )}
            </div>
          </div>
        </>
      )}

      <AdminConfirm
        open={!!pendingDelete}
        title="删除主题"
        message={pendingDelete
          ? `确定要删除主题「${pendingDelete.name}」吗？${pendingDelete.name === activeName ? "此主题当前正在使用，删除后将回退到默认主题。" : ""}此操作不可撤销。`
          : ""}
        confirmText="删除"
        variant="danger"
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />

      <ThemeSettingsModal
        open={!!settingsTarget}
        onClose={() => setSettingsTarget(null)}
        themeName={settingsTarget?.name || ""}
        themeLabel={settingsTarget?.description || settingsTarget?.name || ""}
        csrf={csrf}
      />
    </div>
  );
}

function ThemeCard({
  theme,
  active,
  onActivate,
  onDelete,
  onSettings,
}: {
  theme: Theme;
  active: boolean;
  onActivate: () => void;
  onDelete?: () => void;
  onSettings: () => void;
}) {
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const cssUrl = themePreviewUrl(theme.name, theme.updated_at);
  const hasSettings = (theme.settings?.length || 0) > 0;

  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) return;
    const loadPreview = () => {
      try {
        const doc = iframe.contentDocument;
        if (!doc) return;
        doc.open();
        doc.write(THEME_PREVIEW_HTML);
        doc.close();
        // Inject the theme CSS
        const link = doc.createElement("link");
        link.rel = "stylesheet";
        link.href = cssUrl;
        doc.head.appendChild(link);
      } catch {}
    };
    if (iframe.contentDocument?.readyState === "complete") {
      loadPreview();
    } else {
      iframe.addEventListener("load", loadPreview, { once: true });
    }
  }, [cssUrl]);

  return (
    <div className={`theme-card ${active ? "active" : ""}`}>
      <div className="theme-preview">
        <iframe
          ref={iframeRef}
          srcDoc={THEME_PREVIEW_HTML}
          title={theme.name}
          sandbox="allow-same-origin"
          style={{ width: "100%", height: "100%", border: "none" }}
        />
        {active && <div className="theme-active-badge">✓ 当前使用</div>}
        {theme.builtin && <div className="theme-builtin-badge">内置</div>}
      </div>
      <div className="theme-info">
        <div className="theme-name-row">
          <span className="theme-name">{theme.description || theme.name}</span>
          <code className="theme-slug">{theme.name}</code>
        </div>
        {theme.version && <span className="theme-meta">v{theme.version}</span>}
        {theme.author && <span className="theme-meta">by {theme.author}</span>}
        <div className="theme-date">{fmtDate(theme.updated_at)}</div>
      </div>
      <div className="theme-actions">
        {active ? (
          <button className="admin-btn admin-btn-primary admin-btn-sm" disabled>✓ 使用中</button>
        ) : (
          <button className="admin-btn admin-btn-primary admin-btn-sm" onClick={onActivate}>
            启用
          </button>
        )}
        {hasSettings && (
          <button
            className="admin-btn admin-btn-secondary admin-btn-sm"
            onClick={onSettings}
            title="主题详细设置"
          >
            ⚙ 设置
          </button>
        )}
        {onDelete && (
          <button
            className="admin-btn admin-btn-danger admin-btn-sm"
            onClick={onDelete}
            title="删除主题"
          >
            🗑
          </button>
        )}
      </div>
    </div>
  );
}
