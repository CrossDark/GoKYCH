"use client";

import { useEffect, useState, useRef, useCallback } from "react";
import { AdminModal } from "./AdminModal";
import { getThemeSettings, updateThemeSettings } from "@/lib/api/admin";
import { uploadFile } from "@/lib/api/admin";
import { useToast } from "@/lib/admin-feedback";
import type { SettingDefinition } from "@/lib/types";

interface ThemeSettingsModalProps {
  open: boolean;
  onClose: () => void;
  /** The theme whose settings are being edited. */
  themeName: string;
  /** Human label for the modal title. */
  themeLabel: string;
  /** CSRF token for write calls. */
  csrf: string;
}

// ThemeSettingsModal — admin form for editing per-theme settings.
//
// Renders controls dynamically from the theme's settings SCHEMA (declared
// in theme.yaml's `settings:` block). Each control's value is sent back
// verbatim to PUT /api/admin/themes/:name/settings; the server validates
// every value against the schema and returns 400 + `rejects` if anything
// is off (unknown key, range out of bounds, select not in options).
//
// The `background_image` setting has type=image and uses the existing
// /api/admin/files upload endpoint (same one the rest of the admin uses
// for profile avatars etc.). Other types render as plain inputs.
//
// Theme can be marked as "active" or "not"; we don't gate the modal on
// the active flag — you can configure a theme before activating it to
// preview the look.
export function ThemeSettingsModal({
  open,
  onClose,
  themeName,
  themeLabel,
  csrf,
}: ThemeSettingsModalProps) {
  const toast = useToast();
  const [schema, setSchema] = useState<SettingDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  // working[k] = current edit value for setting k. Always a string (server
  // is the source of truth for type coercion on save). Range inputs render
  // as text so the value-while-typing doesn't have to be normalised.
  const [working, setWorking] = useState<Record<string, string>>({});
  // server[k] = last value the server reported; used by "重置" and to
  // detect which keys have explicit admin overrides (so the UI can
  // distinguish "admin set this to 60" from "admin never touched this,
  // so use the schema default 60").
  const [server, setServer] = useState<Record<string, string>>({});
  const [defaults, setDefaults] = useState<Record<string, string>>({});
  const fileRef = useRef<HTMLInputElement | null>(null);
  // uploadKeyRef (NOT useState) — we need to know which setting key the
  // user is uploading FOR when the file input's change event fires. If
  // we used useState, the handler closure would capture the value from
  // the render in which the upload button was clicked; but the change
  // event fires inside the same tick as the setUploadKey + fileRef.click()
  // sequence, so React hasn't re-rendered yet and the handler sees the
  // OLD uploadKey (null). A ref sidesteps React's render lifecycle:
  // setUploadKey writes synchronously, change handler reads the latest
  // value. The state version is kept only for re-rendering anything
  // that actually depends on it (currently nothing does).
  const uploadKeyRef = useRef<string | null>(null);
  const [, setUploadKeyState] = useState<string | null>(null);
  const setUploadKey = (k: string | null) => {
    uploadKeyRef.current = k;
    setUploadKeyState(k);
  };

  const load = useCallback(async () => {
    if (!open) return;
    setLoading(true);
    try {
      const { schema, values } = await getThemeSettings(themeName);
      setSchema(schema || []);
      setServer(values || {});
      const d: Record<string, string> = {};
      for (const s of schema || []) {
        d[s.key] = s.default == null ? "" : String(s.default);
      }
      setDefaults(d);
      // Seed working = server values falling back to defaults
      const w: Record<string, string> = {};
      for (const s of schema || []) {
        w[s.key] = (values && values[s.key] != null) ? values[s.key] : d[s.key];
      }
      setWorking(w);
    } catch (e: unknown) {
      toast.error((e as Error)?.message || "读取主题设置失败。");
      onClose();
    } finally {
      setLoading(false);
    }
  // We intentionally omit `toast` and `onClose` from deps even though
  // load() references them: useToast() returns a NEW object every
  // render (ToastProvider rebuilds its `api` literal inline), so
  // including `toast` would invalidate `load` on every render, which
  // in turn re-triggers the useEffect below, which would clobber the
  // user's in-progress edits (e.g. an image upload) with stale server
  // values. The toast API methods (success/error) are themselves
  // stable via useCallback, so the stale `toast` reference still works.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, themeName]);

  // Effect deps: only re-fetch when the modal is freshly opened for a
  // different theme. Deliberately NOT depending on `load` directly —
  // see the comment above on why `load` would change every render.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load();
  }, [open, themeName]);

  const setVal = (k: string, v: string) => setWorking((w) => ({ ...w, [k]: v }));

  const handleSave = async () => {
    if (saving) return;
    setSaving(true);
    try {
      // Send ALL schema keys, not just the ones that differ from
      // default. Reason: "切回默认" must be persistent — if the admin
      // had set effect_mode=rain (override) and now picks none, sending
      // nothing would leave the rain override in place. Sending all
      // keys makes the saved value authoritative; one extra row of
      // identical-to-default data is harmless.
      const toSend: Record<string, string> = {};
      for (const s of schema) {
        toSend[s.key] = working[s.key] ?? "";
      }
      await updateThemeSettings(csrf, themeName, toSend);
      // Glass theme runs an in-page effect layer (particles.js) that
      // caches the previous server settings on mount; admin's saved
      // value won't show up until it re-fetches. Window.GlassFX is the
      // global handle particles.js exposes — calling reload() refreshes
      // --glass-card-alpha / --glass-*-frost / effect_mode in-place
      // without a page reload. Other themes don't have a runtime
      // effect layer, so this is a glass-specific no-op for them.
      if (typeof window !== "undefined" && (window as unknown as { GlassFX?: { reload: () => void } }).GlassFX?.reload) {
        (window as unknown as { GlassFX?: { reload: () => void } }).GlassFX!.reload();
      }
      toast.success(`「${themeLabel}」设置已保存。`);
      onClose();
    } catch (e: unknown) {
      const err = e as { data?: { rejects?: string[] }; message?: string };
      if (err.data && Array.isArray(err.data.rejects) && err.data.rejects.length > 0) {
        toast.error(`保存失败：${err.data.rejects[0]}`);
      } else {
        toast.error(err.message || "保存设置失败。");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleReset = () => {
    // Reset to schema defaults (i.e. clear all admin overrides).
    setWorking({ ...defaults });
  };

  const handleFilePicked = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    // Read from the REF, not the state — see comment on uploadKeyRef.
    const key = uploadKeyRef.current;
    if (!key) return;
    try {
      const res = await uploadFile(csrf, f);
      setVal(key, res.url);
      toast.success("图片已上传。");
    } catch (err: unknown) {
      toast.error((err as Error)?.message || "上传失败。");
    } finally {
      if (fileRef.current) fileRef.current.value = "";
      setUploadKey(null);
    }
  };

  return (
    <AdminModal
      open={open}
      onClose={onClose}
      title={`设置 — ${themeLabel}`}
      size="md"
      persistent={saving}
      footer={
        <>
          <button
            type="button"
            className="admin-btn admin-btn-secondary"
            onClick={handleReset}
            disabled={loading || saving}
          >
            重置为默认
          </button>
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="admin-btn admin-btn-secondary"
            onClick={onClose}
            disabled={saving}
          >
            取消
          </button>
          <button
            type="button"
            className="admin-btn admin-btn-primary"
            onClick={handleSave}
            disabled={loading || saving}
          >
            {saving ? "保存中…" : "保存"}
          </button>
        </>
      }
    >
      {loading ? (
        <div className="admin-empty" style={{ padding: "20px" }}>加载中…</div>
      ) : schema.length === 0 ? (
        <div className="admin-empty" style={{ padding: "20px" }}>
          <div className="admin-empty-title">该主题没有可配置的设置</div>
          <div>主题的 theme.yaml 没有声明 settings schema</div>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
          {schema.map((s) => {
            const v = working[s.key] ?? "";
            const isOverridden = server[s.key] != null;
            return (
              <div key={s.key} className="form-group">
                <label className="form-label" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  {s.label}
                  {isOverridden && (
                    <span style={{
                      fontSize: "0.7rem",
                      padding: "1px 6px",
                      borderRadius: 4,
                      background: "var(--admin-primary-soft)",
                      color: "var(--admin-primary)",
                    }}>
                      已自定义
                    </span>
                  )}
                </label>
                <SettingControl
                  def={s}
                  value={v}
                  disabled={saving}
                  onChange={(val) => setVal(s.key, val)}
                  onUploadClick={() => {
                    setUploadKey(s.key);
                    fileRef.current?.click();
                  }}
                />
                {s.hint && <div className="form-hint">{s.hint}</div>}
              </div>
            );
          })}
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            style={{ display: "none" }}
            onChange={handleFilePicked}
          />
        </div>
      )}
    </AdminModal>
  );
}

// SettingControl — one row of the schema-driven form. Renders the right
// control for the setting's type. Kept in the same file so the schema
// shape and the rendering stay in sync.
function SettingControl({
  def,
  value,
  disabled,
  onChange,
  onUploadClick,
}: {
  def: SettingDefinition;
  value: string;
  disabled: boolean;
  onChange: (v: string) => void;
  onUploadClick: () => void;
}) {
  if (def.type === "select") {
    return (
      <select
        className="admin-input"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      >
        {(def.options || []).map((o) => (
          <option key={o} value={o}>{o}</option>
        ))}
      </select>
    );
  }
  if (def.type === "range") {
    const min = def.min ?? 0;
    const max = def.max ?? 100;
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <input
          type="range"
          min={min}
          max={max}
          step={def.step ?? 1}
          value={value || min}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          style={{ flex: 1 }}
        />
        <span style={{
          minWidth: 40,
          textAlign: "right",
          fontFamily: "var(--font-mono, monospace)",
          fontSize: "0.85rem",
          color: "var(--text-secondary)",
        }}>
          {value || min}
        </span>
      </div>
    );
  }
  if (def.type === "image") {
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <input
          type="text"
          className="admin-input"
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          placeholder="上传图片 或 粘贴 URL"
          style={{ flex: 1 }}
        />
        <button
          type="button"
          className="admin-btn admin-btn-secondary"
          disabled={disabled}
          onClick={onUploadClick}
        >
          上传
        </button>
        {value && (
          <button
            type="button"
            className="admin-btn admin-btn-secondary"
            disabled={disabled}
            onClick={() => onChange("")}
            title="清除"
          >
            ✕
          </button>
        )}
      </div>
    );
  }
  // text (default)
  return (
    <input
      type="text"
      className="admin-input"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      placeholder={def.hint || ""}
    />
  );
}
