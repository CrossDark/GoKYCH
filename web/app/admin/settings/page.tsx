"use client";

import { useState, useEffect } from "react";
import { getCsrf, getSettings, updateSettings } from "@/lib/api";

// Chinese translations for section names and field keys
const SECTION_LABELS: Record<string, string> = {
  site: "站点信息",
  appearance: "外观设置",
  features: "功能开关",
  social: "社交媒体",
};

const FIELD_LABELS: Record<string, string> = {
  // site
  title: "网站标题",
  subtitle: "副标题",
  description: "网站描述",
  language: "语言",
  timezone: "时区",
  logo_path: "Logo 路径",
  favicon_path: "Favicon 路径",
  icp_number: "ICP 备案号",
  // appearance
  font_family: "字体",
  primary_color: "主题色",
  style_theme: "样式主题",
  theme: "默认主题 (auto/light/dark)",
  // features
  enable_comments: "启用评论",
  enable_dark_mode: "启用暗黑模式",
  enable_search: "启用搜索",
  enable_tags_sidebar: "启用标签侧栏",
  posts_per_page: "每页文章数",
  // social
  email: "邮箱",
  github: "GitHub",
  twitter: "Twitter",
};

// Order of sections (top to bottom)
const SECTION_ORDER = ["site", "appearance", "features", "social"];

export default function AdminSettings() {
  const [csrf, setCsrf] = useState("");
  const [settings, setSettings] = useState<Record<string, any> | null>(null);
  const [msg, setMsg] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    getCsrf().then((r) => {
      setCsrf(r.csrf_token);
      getSettings(r.csrf_token).then((s) => {
        setSettings(s);
        setLoading(false);
      }).catch(() => setLoading(false));
    });
  }, []);

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
    setMsg("");
    try {
      await updateSettings(csrf, settings);
      setMsg("设置已保存。");
    } catch (err: any) {
      setMsg(err.message || "保存失败。");
    }
  };

  if (loading) return <div className="admin-settings"><p>加载中…</p></div>;
  if (!settings) return <div className="admin-settings"><p>无法加载设置。</p></div>;

  return (
    <div className="admin-settings">
      <h1>站点设置</h1>
      {msg && <p className="admin-msg">{msg}</p>}

      {SECTION_ORDER.map((section) => {
        const fields = settings[section];
        if (!fields) return null;
        return (
          <div key={section} className="admin-settings-section">
            <h2>{SECTION_LABELS[section] || section}</h2>
            {Object.entries(fields as Record<string, any>).map(([key, value]) => (
              <label key={key} className="admin-setting-field">
                <span className="admin-setting-key">
                  {FIELD_LABELS[key] || key}
                </span>
                {typeof value === "boolean" ? (
                  <input
                    type="checkbox"
                    checked={value}
                    onChange={(e) => handleChange(section, key, e.target.checked)}
                  />
                ) : (
                  <input
                    type="text"
                    value={String(value ?? "")}
                    onChange={(e) => handleTextChange(section, key, value, e.target.value)}
                  />
                )}
              </label>
            ))}
          </div>
        );
      })}

      <button onClick={handleSave} className="btn btn-primary">保存设置</button>
    </div>
  );
}
