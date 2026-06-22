"use client";

import { useState, useEffect } from "react";
import { getCsrf, getSettings, updateSettings } from "@/lib/api";

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
    // Preserve original type: numbers stay numbers, others become strings
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

      {Object.entries(settings).map(([section, fields]) => (
        <div key={section} className="admin-settings-section">
          <h2>{section}</h2>
          {Object.entries(fields as Record<string, any>).map(([key, value]) => (
            <label key={key} className="admin-setting-field">
              <span className="admin-setting-key">{key}</span>
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
      ))}

      <button onClick={handleSave} className="btn btn-primary">保存设置</button>
    </div>
  );
}
