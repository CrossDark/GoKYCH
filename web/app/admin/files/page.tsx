"use client";

import { useEffect, useRef, useState } from "react";
import {
  getCsrf,
  listAdminFiles,
  uploadFile,
  deleteAdminFile,
} from "@/lib/api";
import type { AdminFile } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { fmtDateTime } from "@/lib/format";

const IMAGE_EXTS = /\.(png|jpe?g|gif|webp|svg|ico|bmp)$/i;

export default function AdminFiles() {
  const [csrf, setCsrf] = useState("");
  const [files, setFiles] = useState<AdminFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const toast = useToast();
  const [dragOver, setDragOver] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<AdminFile | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const load = () => {
    if (!csrf) return;
    setLoading(true);
    listAdminFiles(csrf)
      .then(setFiles)
      .catch(() => setFiles([]))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token));
  }, []);

  useEffect(() => {
    if (csrf) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [csrf]);

  const doUpload = async (picked: FileList | null) => {
    if (!picked || picked.length === 0) return;
    setUploading(true);
    let ok = 0;
    let fail = 0;
    let dedup = 0;
    const failures: string[] = [];
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
    setUploading(false);
    if (inputRef.current) inputRef.current.value = "";
    load();
  };

  const handleSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    doUpload(e.target.files);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    doUpload(e.dataTransfer.files);
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
      load();
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      throw err;
    } finally {
      setPendingDelete(null);
    }
  };

  const fmtSize = (n: number) => {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
  };

  return (
    <div className="admin-files">
      <div className="admin-page-header">
        <div>
          <h1>文件管理</h1>
          <div className="admin-page-subtitle">
            {files.length > 0 ? `共 ${files.length} 个文件` : "加载中…"}
          </div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>⬆ 上传文件</h2>
        </div>
        <div className="admin-card-body">
          <div
            className={`admin-upload-dropzone ${dragOver ? "drag-over" : ""}`}
            onDragOver={(e) => {
              e.preventDefault();
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
            onClick={() => inputRef.current?.click()}
          >
            <input
              ref={inputRef}
              type="file"
              multiple
              style={{ display: "none" }}
              onChange={handleSelect}
              disabled={uploading}
            />
            <div className="admin-upload-prompt">
              {uploading ? (
                <p>
                  <span className="admin-spinner" style={{ marginRight: 6, verticalAlign: "middle" }} />
                  上传中…
                </p>
              ) : (
                <>
                  <p className="admin-upload-title">点击选择文件 或 拖拽到此处</p>
                  <p className="admin-upload-hint">
                    支持 png / jpg / gif / webp / svg / ico / pdf / md / txt / css · 单文件最大 10 MB
                  </p>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

      {loading ? (
        <div className="admin-card"><div className="admin-card-body"><div className="admin-empty">加载中…</div></div></div>
      ) : files.length === 0 ? (
        <div className="admin-card"><div className="admin-card-body">
          <div className="admin-empty">
            <span className="admin-empty-icon">📁</span>
            <div className="admin-empty-title">还没有上传文件</div>
            <div>通过上方拖拽区上传</div>
          </div>
        </div></div>
      ) : (
        <div className="admin-card">
          <div className="admin-card-header">
            <h2>📁 文件列表</h2>
          </div>
          <div className="admin-card-body no-padding">
            <table className="admin-table">
              <thead>
                <tr>
                  <th style={{ width: 60 }}>预览</th>
                  <th>文件名</th>
                  <th>大小</th>
                  <th>类型</th>
                  <th className="col-date">上传时间</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {files.map((f) => {
                  // The API now returns a fully-qualified `url` (PUBLIC_URL +
                  // /uploads/<filename> in prod, /uploads/<filename> in dev).
                  // We use it directly so cross-origin deployments (CF Pages
                  // + separate API host) don't 404 on relative paths.
                  const url = f.url || `/uploads/${f.filename}`;
                  const isImage = IMAGE_EXTS.test(f.filename) ||
                    (f.mime_type?.startsWith("image/") ?? false);
                  return (
                    <tr key={f.id}>
                      <td className="admin-file-thumb">
                        {isImage ? (
                          <img
                            src={url}
                            alt={f.original_name || f.filename}
                            loading="lazy"
                          />
                        ) : (
                          <span className="admin-file-icon">📄</span>
                        )}
                      </td>
                      <td>
                        <div className="admin-file-name">{f.original_name || f.filename}</div>
                        <div className="admin-file-stored">{f.filename}</div>
                      </td>
                      <td>{fmtSize(f.file_size)}</td>
                      <td><span style={{ fontSize: "0.78rem", color: "var(--text-muted)" }}>{f.mime_type || "—"}</span></td>
                      <td className="col-date">{fmtDateTime(f.created_at)}</td>
                      <td className="col-actions">
                        <button className="admin-btn admin-btn-outline admin-btn-sm" onClick={() => copyUrl(url)} title="复制链接">📋</button>
                        <a
                          className="admin-btn admin-btn-secondary admin-btn-sm"
                          href={url}
                          target="_blank"
                          rel="noopener noreferrer"
                          title="打开"
                        >↗</a>
                        <button
                          className="admin-btn admin-btn-danger admin-btn-sm"
                          onClick={() => handleDelete(f)}
                          title="删除"
                        >🗑</button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

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