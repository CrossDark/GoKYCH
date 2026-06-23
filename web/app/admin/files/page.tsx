"use client";

import { useEffect, useRef, useState } from "react";
import {
  getCsrf,
  listAdminFiles,
  uploadFile,
  deleteAdminFile,
} from "@/lib/api";
import type { AdminFile } from "@/lib/types";

const IMAGE_EXTS = /\.(png|jpe?g|gif|webp|svg|ico|bmp)$/i;

export default function AdminFiles() {
  const [csrf, setCsrf] = useState("");
  const [files, setFiles] = useState<AdminFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [msg, setMsg] = useState("");
  const [dragOver, setDragOver] = useState(false);
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
    setMsg("");
    setUploading(true);
    let ok = 0;
    let fail = 0;
    let dedup = 0;
    for (const f of Array.from(picked)) {
      try {
        const r = await uploadFile(csrf, f);
        if (r.deduped) dedup++;
        else ok++;
      } catch (err: any) {
        fail++;
        // Continue with the rest — one bad file shouldn't block the batch.
        console.error("upload failed:", f.name, err);
      }
    }
    setMsg(
      `上传完成：${ok} 个新增，${dedup} 个去重，${fail} 个失败。`,
    );
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
      setMsg(`已复制链接：${url}`);
    } catch {
      // Fallback for browsers without clipboard permission.
      setMsg(`链接：${url}（请手动复制）`);
    }
  };

  const handleDelete = async (f: AdminFile) => {
    if (!confirm(`确定删除「${f.original_name || f.filename}」吗？此操作不可撤销。`))
      return;
    setMsg("");
    try {
      await deleteAdminFile(csrf, f.id);
      setMsg(`已删除「${f.original_name || f.filename}」。`);
      load();
    } catch (err: any) {
      setMsg(err.message || "删除失败。");
    }
  };

  const fmtSize = (n: number) => {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
  };

  return (
    <div className="admin-files">
      <h1>文件管理</h1>
      <p className="admin-hint">
        上传图片、附件供文章和设置使用。支持拖拽，单文件最大 10 MB，仅允许常见图片与文档类型。
      </p>

      {msg && <p className="admin-msg">{msg}</p>}

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
        />
        <div className="admin-upload-prompt">
          {uploading ? (
            <p>上传中…</p>
          ) : (
            <>
              <p className="admin-upload-title">点击选择文件 或 拖拽到此处</p>
              <p className="admin-upload-hint">
                支持 png / jpg / gif / webp / svg / ico / pdf / md / txt / css
              </p>
            </>
          )}
        </div>
      </div>

      {loading ? (
        <p className="loading">加载中…</p>
      ) : files.length === 0 ? (
        <p className="empty-message">还没有上传文件。</p>
      ) : (
        <table className="admin-table">
          <thead>
            <tr>
              <th>预览</th>
              <th>文件名</th>
              <th>大小</th>
              <th>类型</th>
              <th>上传时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {files.map((f) => {
              const url = `/uploads/${f.filename}`;
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
                  <td>{f.mime_type || "—"}</td>
                  <td>{new Date(f.created_at).toLocaleString("zh-CN")}</td>
                  <td>
                    <button className="btn btn-small" onClick={() => copyUrl(url)}>
                      复制链接
                    </button>
                    <a
                      className="btn btn-small"
                      href={url}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      打开
                    </a>
                    <button
                      className="btn btn-small btn-danger"
                      onClick={() => handleDelete(f)}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}