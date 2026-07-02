"use client";

import { useState, useEffect, useRef } from "react";
import { getCsrf, getMe, getSettings, updateSettings, listThemes, listAdminFiles } from "@/lib/api";
import type { Theme, SiteSettings, AdminFile, User } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";

const SECTION_LABELS: Record<string, string> = {
  site: "站点信息",
  appearance: "外观设置",
  features: "功能开关",
};

const FIELD_LABELS: Record<string, string> = {
  title: "网站标题",
  subtitle: "副标题",
  description: "网站描述",
  language: "语言",
  timezone: "时区",
  logo_path: "Logo 路径",
  favicon_path: "Favicon 路径",
  icp_number: "ICP 备案号",
  font_family: "字体",
  primary_color: "主题色",
  style_theme: "样式主题",
  theme: "默认主题 (auto/light/dark)",
  enable_comments: "启用评论",
  enable_dark_mode: "启用暗黑模式",
  enable_search: "启用搜索",
  enable_tags_sidebar: "启用标签侧栏",
  posts_per_page: "每页文章数",
  allow_all_edit: "允许所有用户编辑所有文章（站长专属）",
};

const SECTION_ORDER = ["site", "appearance", "features"];

const COLOR_FIELDS = ["primary_color"];
const SELECT_FIELDS: Record<string, { value: string; label: string }[]> = {
  theme: [
    { value: "auto", label: "跟随系统" },
    { value: "light", label: "浅色模式" },
    { value: "dark", label: "深色模式" },
  ],
  language: [
    { value: "zh-CN", label: "简体中文" },
    { value: "en", label: "English" },
  ],
  timezone: [
    { value: "Asia/Shanghai", label: "Asia/Shanghai" },
    { value: "UTC", label: "UTC" },
  ],
};

export default function AdminSettings() {
  const [csrf, setCsrf] = useState("");
  const [me, setMe] = useState<User | null>(null);
  const [settings, setSettings] = useState<SiteSettings | null>(null);
  const initialSettingsRef = useRef<SiteSettings | null>(null);
  const [availableThemes, setAvailableThemes] = useState<Theme[]>([]);
  const toast = useToast();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const isDirty = initialSettingsRef.current !== null
    && settings !== null
    && JSON.stringify(settings) !== JSON.stringify(initialSettingsRef.current);

  const [filePickerOpen, setFilePickerOpen] = useState(false);
  const [filePickerTarget, setFilePickerTarget] = useState<string>("");
  const [uploadedFiles, setUploadedFiles] = useState<AdminFile[]>([]);

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getSettings(r.csrf_token).then((s) => {
        setSettings(s);
        initialSettingsRef.current = s;
        setLoading(false);
      }).catch(() => setLoading(false));
      listAdminFiles(r.csrf_token).then(setUploadedFiles).catch(() => {});
    });
    getMe().then((r) => {
      if (r.user) setMe(r.user);
    }).catch(() => {});
    listThemes()
      .then((themes) => {
        setAvailableThemes(themes.filter((t) => t.has_css));
      })
      .catch(() => setAvailableThemes([]));
  }, []);

  useBeforeUnload(isDirty && !saving);

  const refreshFiles = async (token: string) => {
    try {
      const files = await listAdminFiles(token);
      setUploadedFiles(files);
    } catch {}
  };

  const openFilePicker = (section: string, key: string) => {
    setFilePickerTarget(`${section}.${key}`);
    setFilePickerOpen(true);
    if (csrf) {
      refreshFiles(csrf);
    }
  };

  const selectFile = (filename: string) => {
    if (!settings || !filePickerTarget) return;
    const [section, key] = filePickerTarget.split(".");
    const path = `/uploads/${filename}`;
    setSettings({
      ...settings,
      [section]: { ...(settings as any)[section], [key]: path },
    });
    setFilePickerOpen(false);
  };

  const handleChange = (section: string, key: string, value: any) => {
    if (!settings) return;
    setSettings({
      ...settings,
      [section]: { ...(settings as any)[section], [key]: value },
    });
  };

  const handleTextChange = (section: string, key: string, originalValue: any, newValue: string) => {
    if (!settings) return;
    let converted: any = newValue;
    if (typeof originalValue === "number") {
      const n = parseInt(newValue, 10);
      converted = isNaN(n) ? originalValue : n;
    }
    setSettings({
      ...settings,
      [section]: { ...(settings as any)[section], [key]: converted },
    });
  };

  const handleSave = async () => {
    if (!settings) return;
    setSaving(true);
    try {
      await updateSettings(csrf, settings);
      initialSettingsRef.current = settings;
      toast.success("设置已保存。");
    } catch (err: any) {
      toast.error(err.message || "保存失败。");
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <div className="admin-settings"><p>加载中…</p></div>;
  if (!settings) return <div className="admin-settings"><p>无法加载设置。</p></div>;

  const renderField = (section: string, key: string, value: any) => {
    const label = FIELD_LABELS[key] || key;

    if (COLOR_FIELDS.includes(key)) {
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <div className="color-picker-wrap">
            <input
              type="color"
              value={String(value ?? "#3b82f6")}
              onChange={(e) => handleChange(section, key, e.target.value)}
            />
            <input
              type="text"
              value={String(value ?? "")}
              onChange={(e) => handleTextChange(section, key, value, e.target.value)}
              className="color-text-input"
            />
          </div>
        </label>
      );
    }

    if (SELECT_FIELDS[key]) {
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <select
            value={String(value ?? "")}
            onChange={(e) => handleChange(section, key, e.target.value)}
          >
            {SELECT_FIELDS[key].map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </label>
      );
    }

    if (key === "style_theme") {
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <select
            value={String(value ?? "")}
            onChange={(e) => handleChange(section, key, e.target.value)}
          >
            <option value="">（使用内置默认）</option>
            {availableThemes.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}{t.description ? ` — ${t.description}` : ""}
              </option>
            ))}
          </select>
          <span className="admin-setting-hint">
            主题目录：<code>data/themes/&lt;name&gt;/static/theme.css</code>。
            重启后端后新建的主题才会出现在列表中。
          </span>
        </label>
      );
    }

    if (key === "logo_path" || key === "favicon_path") {
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <div className="file-picker-wrap">
            <input
              type="text"
              value={String(value ?? "")}
              onChange={(e) => handleTextChange(section, key, value, e.target.value)}
            />
            <button
              type="button"
              className="admin-btn admin-btn-ghost admin-btn-sm"
              onClick={() => openFilePicker(section, key)}
              title="从已上传文件中选择"
            >
              📁 浏览
            </button>
          </div>
          {String(value ?? "") && (
            <div className="file-preview">
              <img src={String(value)} alt="预览" onError={(e) => { (e.target as HTMLImageElement).style.display = "none"; }} />
            </div>
          )}
        </label>
      );
    }

    if (typeof value === "boolean") {
      const isOwnerOnly = key === "allow_all_edit";
      const isOwner = me?.role === "owner";
      if (isOwnerOnly && !isOwner) {
        return null;
      }
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <input
            type="checkbox"
            checked={value}
            disabled={isOwnerOnly && !isOwner}
            onChange={(e) => handleChange(section, key, e.target.checked)}
          />
          {isOwnerOnly && (
            <span className="admin-setting-hint">
              开启后所有已登录用户都可以编辑、删除任意文章（包括管理员创建的文章），请谨慎操作。仅站长（owner）可修改此设置。
            </span>
          )}
        </label>
      );
    }

    return (
      <label key={key} className="admin-setting-field">
        <span className="admin-setting-key">{label}</span>
        <input
          type={typeof value === "number" ? "number" : "text"}
          value={String(value ?? "")}
          onChange={(e) => handleTextChange(section, key, value, e.target.value)}
        />
      </label>
    );
  };

  return (
    <div className="admin-settings">
      <div className="admin-page-header">
        <div>
          <h1>站点设置</h1>
          <div className="admin-page-subtitle">站点信息、外观、功能开关</div>
        </div>
      </div>

      {SECTION_ORDER.map((section) => {
        const fields = (settings as any)[section];
        if (!fields) return null;
        return (
          <section key={section} className="admin-card">
            <div className="admin-card-header">
              <h2>{SECTION_LABELS[section] || section}</h2>
            </div>
            <div className="admin-card-body">
              {Object.entries(fields as Record<string, any>).map(([key, value]) =>
                renderField(section, key, value)
              )}
            </div>
          </section>
        );
      })}

      <button
        onClick={handleSave}
        className={`admin-btn admin-btn-primary admin-btn-lg ${saving ? "admin-btn-loading" : ""}`}
        disabled={saving}
      >
        💾 保存设置
      </button>

      <AdminModal
        open={filePickerOpen}
        onClose={() => setFilePickerOpen(false)}
        title="📁 选择文件"
      >
        {uploadedFiles.length === 0 ? (
          <p className="empty-message">暂无已上传文件。请先在文件管理中上传。</p>
        ) : (
          <div className="file-grid">
            {uploadedFiles.map((f) => (
              <div
                key={f.id}
                className="file-grid-item"
                onClick={() => selectFile(f.filename)}
                title={f.original_name || f.filename}
              >
                <div className="file-grid-preview">
                  {f.filename.match(/\.(png|jpg|jpeg|gif|webp|svg|ico)$/i) ? (
                    <img src={f.url || `/uploads/${f.filename}`} alt={f.original_name} />
                  ) : (
                    <span className="file-grid-icon">📄</span>
                  )}
                </div>
                <span className="file-grid-name">{f.original_name || f.filename}</span>
              </div>
            ))}
          </div>
        )}
      </AdminModal>
    </div>
  );
}