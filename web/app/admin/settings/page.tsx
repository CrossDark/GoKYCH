"use client";

import { useState, useEffect, useRef } from "react";
import { getCsrf, getSettings, updateSettings } from "@/lib/api";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";

// Chinese translations for section names and field keys
const SECTION_LABELS: Record<string, string> = {
  site: "站点信息",
  appearance: "外观设置",
  features: "功能开关",
  social: "社交媒体",
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
  email: "邮箱",
  github: "GitHub",
  twitter: "Twitter",
};

const SECTION_ORDER = ["site", "appearance", "features", "social"];

// Fields that should use special input types
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
  style_theme: [
    { value: "default", label: "默认 (蓝白)" },
    { value: "sunset", label: "日落" },
    { value: "forest", label: "森林" },
    { value: "ocean", label: "海洋" },
  ],
};

export default function AdminSettings() {
  const [csrf, setCsrf] = useState("");
  const [settings, setSettings] = useState<Record<string, any> | null>(null);
  const initialSettingsRef = useRef<Record<string, any> | null>(null);
  const toast = useToast();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const isDirty = initialSettingsRef.current !== null
    && settings !== null
    && JSON.stringify(settings) !== JSON.stringify(initialSettingsRef.current);

  // File picker state
  const [filePickerOpen, setFilePickerOpen] = useState(false);
  const [filePickerTarget, setFilePickerTarget] = useState<string>("");
  const [uploadedFiles, setUploadedFiles] = useState<{ filename: string; original_name: string; id: number }[]>([]);

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getSettings(r.csrf_token).then((s) => {
        setSettings(s);
        initialSettingsRef.current = s;
        setLoading(false);
      }).catch(() => setLoading(false));
      // Load uploaded files for file picker
      fetch(`/api/admin/files`, { headers: { "X-CSRF-Token": r.csrf_token } })
        .then(res => res.json())
        .then(setUploadedFiles)
        .catch(() => {});
    });
  }, []);

  useBeforeUnload(isDirty && !saving);

  const openFilePicker = (section: string, key: string) => {
    setFilePickerTarget(`${section}.${key}`);
    setFilePickerOpen(true);
    // Refresh file list
    if (csrf) {
      fetch(`/api/admin/files`, { headers: { "X-CSRF-Token": csrf } })
        .then(res => res.json())
        .then(setUploadedFiles)
        .catch(() => {});
    }
  };

  const selectFile = (filename: string) => {
    if (!settings || !filePickerTarget) return;
    const [section, key] = filePickerTarget.split(".");
    const path = `/uploads/${filename}`;
    setSettings({
      ...settings,
      [section]: { ...settings[section], [key]: path },
    });
    setFilePickerOpen(false);
  };

  const handleChange = (section: string, key: string, value: any) => {
    if (!settings) return;
    setSettings({
      ...settings,
      [section]: { ...settings[section], [key]: value },
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
      [section]: { ...settings[section], [key]: converted },
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

    // Color picker
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

    // Select dropdown
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

    // File path fields
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

    // Boolean toggle
    if (typeof value === "boolean") {
      return (
        <label key={key} className="admin-setting-field">
          <span className="admin-setting-key">{label}</span>
          <input
            type="checkbox"
            checked={value}
            onChange={(e) => handleChange(section, key, e.target.checked)}
          />
        </label>
      );
    }

    // Default: text input
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
          <div className="admin-page-subtitle">站点信息、外观、功能开关、社交媒体</div>
        </div>
      </div>

      {SECTION_ORDER.map((section) => {
        const fields = settings[section];
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

      {/* File Picker Modal */}
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
                    <img src={`/uploads/${f.filename}`} alt={f.original_name} />
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
