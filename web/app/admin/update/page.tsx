"use client";

import { useState, useEffect, useCallback } from "react";
import { getCsrf, apiUrl, apiFetch } from "@/lib/api";
import { useToast } from "@/lib/admin-feedback";

interface UpdateCheckInfo {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  platform: string;
  os: string;
  arch: string;
  binary_path: string;
  can_write: boolean;
  published_at?: string;
  release_url?: string;
  release_notes?: string;
  download_size?: number;
  error?: string;
}

interface ApplyResult {
  success: boolean;
  message: string;
  version?: string;
  old_backup?: string;
  restarting: boolean;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

export default function AdminUpdate() {
  const toast = useToast();
  const [csrf, setCsrf] = useState("");
  const [info, setInfo] = useState<UpdateCheckInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);

  const addLog = useCallback((msg: string) => {
    const ts = new Date().toLocaleTimeString("zh-CN", { hour12: false });
    setLogs((prev) => [...prev, `[${ts}] ${msg}`]);
  }, []);

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token)).catch(() => {});
  }, []);

  const check = useCallback(async () => {
    setChecking(true);
    setLogs([]);
    addLog("正在检查 GitHub 最新 Release...");
    try {
      const res = await apiFetch(apiUrl("/api/admin/update/check"));
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      const data: UpdateCheckInfo = await res.json();
      setInfo(data);
      if (data.error) {
        addLog(`检查完成但有警告: ${data.error}`);
      } else if (data.update_available) {
        addLog(`发现新版本 ${data.latest_version}`);
      } else {
        addLog(`当前版本 ${data.current_version} 已是最新`);
      }
    } catch (e: any) {
      addLog(`检查失败: ${e.message}`);
      toast.error("检查更新失败: " + e.message);
    } finally {
      setChecking(false);
    }
  }, [addLog, toast]);

  // Auto-check on mount
  useEffect(() => {
    if (csrf) check();
  }, [csrf, check]);

  const apply = useCallback(async () => {
    if (!info?.update_available) return;
    if (!info.can_write) {
      toast.error("二进制目录不可写，请检查进程权限");
      return;
    }
    if (!confirm(`确定要更新到 ${info.latest_version} 吗？\n\n更新过程中服务将短暂重启（约 2-5 秒）。当前二进制会备份为 ${info.binary_path}.prev。`)) {
      return;
    }
    setApplying(true);
    addLog(`开始下载 ${info.latest_version}...`);
    try {
      const res = await apiFetch(apiUrl("/api/admin/update/apply"), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": csrf,
        },
        body: JSON.stringify({}),
      });
      const data: ApplyResult = await res.json();
      if (!res.ok || !data.success) {
        throw new Error((data as any).error || data.message || `HTTP ${res.status}`);
      }
      addLog(`✅ ${data.message}`);
      if (data.old_backup) {
        addLog(`旧版本已备份到: ${data.old_backup}`);
      }
      if (data.restarting) {
        addLog("服务正在重启，页面将在 5 秒后刷新...");
        setTimeout(() => window.location.reload(), 5000);
      }
      toast.success(data.message);
    } catch (e: any) {
      addLog(`❌ 更新失败: ${e.message}`);
      toast.error("更新失败: " + e.message);
    } finally {
      setApplying(false);
    }
  }, [info, csrf, addLog, toast]);

  return (
    <div className="admin-page">
      <h1 className="admin-page-title">系统更新</h1>
      <p className="admin-page-desc">
        从 GitHub Release 自动检测最新版本，下载匹配当前平台的二进制并热重启服务。仅站点所有者可执行。
      </p>

      <div className="admin-card" style={{ maxWidth: 720 }}>
        <div className="admin-card-body" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
          {/* Status panel */}
          <div style={{
            display: "grid",
            gridTemplateColumns: "120px 1fr",
            gap: "0.5rem 1rem",
            fontSize: "0.9rem",
          }}>
            <div style={{ color: "var(--text-muted)" }}>当前版本</div>
            <div>
              <code style={{
                background: "var(--surface-2)",
                padding: "0.15rem 0.5rem",
                borderRadius: 4,
                fontWeight: 600,
              }}>{info?.current_version || "…"}</code>
            </div>

            <div style={{ color: "var(--text-muted)" }}>最新版本</div>
            <div>
              {info ? (
                info.update_available ? (
                  <span>
                    <code style={{
                      background: "var(--accent)",
                      color: "#fff",
                      padding: "0.15rem 0.5rem",
                      borderRadius: 4,
                      fontWeight: 600,
                    }}>{info.latest_version}</code>
                    {" "}<span style={{ color: "var(--accent)", fontWeight: 600 }}>可更新</span>
                  </span>
                ) : info.error ? (
                  <span style={{ color: "var(--text-muted)" }}>未知（{info.error}）</span>
                ) : (
                  <span style={{ color: "#22c55e", fontWeight: 600 }}>✓ 已是最新</span>
                )
              ) : checking ? (
                <span style={{ color: "var(--text-muted)" }}>检查中…</span>
              ) : "—"}
            </div>

            <div style={{ color: "var(--text-muted)" }}>运行平台</div>
            <div>
              {info ? (
                <>
                  <code>{info.platform}</code>
                  {" "}<span style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>
                    ({info.os} / {info.arch})
                  </span>
                </>
              ) : "检测中…"}
            </div>

            <div style={{ color: "var(--text-muted)" }}>二进制路径</div>
            <div>
              <code style={{
                background: "var(--surface-2)",
                padding: "0.15rem 0.5rem",
                borderRadius: 4,
                fontSize: "0.82rem",
                wordBreak: "break-all",
              }}>{info?.binary_path || "检测中…"}</code>
              {info && (
                <span style={{
                  marginLeft: "0.5rem",
                  fontSize: "0.8rem",
                  color: info.can_write ? "#22c55e" : "#ef4444",
                }}>
                  {info.can_write ? "✓ 可写" : "✗ 不可写（权限不足）"}
                </span>
              )}
            </div>

            {info?.published_at && (
              <>
                <div style={{ color: "var(--text-muted)" }}>发布时间</div>
                <div>{new Date(info.published_at).toLocaleString("zh-CN")}</div>
              </>
            )}

            {info?.download_size ? (
              <>
                <div style={{ color: "var(--text-muted)" }}>下载大小</div>
                <div>{formatBytes(info.download_size)}</div>
              </>
            ) : null}
          </div>

          {/* Release notes */}
          {info?.release_notes && info.update_available && (
            <div>
              <div style={{ fontWeight: 600, marginBottom: "0.4rem", fontSize: "0.9rem" }}>
                Release Notes
              </div>
              <pre style={{
                background: "var(--surface-2)",
                padding: "0.75rem 1rem",
                borderRadius: 6,
                fontSize: "0.82rem",
                maxHeight: 200,
                overflow: "auto",
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                margin: 0,
                fontFamily: "inherit",
                lineHeight: 1.5,
              }}>{info.release_notes}</pre>
              {info.release_url && (
                <a href={info.release_url} target="_blank" rel="noopener noreferrer"
                   style={{ fontSize: "0.8rem", color: "var(--accent)" }}>
                  在 GitHub 查看 →
                </a>
              )}
            </div>
          )}

          {/* Action buttons */}
          <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap" }}>
            <button
              className="btn"
              onClick={check}
              disabled={checking || applying}
            >
              {checking ? "检查中…" : "🔄 检查更新"}
            </button>
            <button
              className="btn btn-primary"
              onClick={apply}
              disabled={!info?.update_available || checking || applying || !info?.can_write}
              title={!info?.can_write ? "二进制目录不可写" : ""}
            >
              {applying ? "更新中…" : info?.update_available ? `⬆️ 更新到 ${info.latest_version}` : "已是最新版本"}
            </button>
          </div>

          {/* Log panel */}
          {logs.length > 0 && (
            <div>
              <div style={{ fontWeight: 600, marginBottom: "0.4rem", fontSize: "0.9rem" }}>日志</div>
              <pre style={{
                background: "#0f172a",
                color: "#e2e8f0",
                padding: "0.75rem 1rem",
                borderRadius: 6,
                fontSize: "0.8rem",
                maxHeight: 200,
                overflow: "auto",
                margin: 0,
                fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
                lineHeight: 1.6,
              }}>{logs.join("\n")}</pre>
            </div>
          )}
        </div>
      </div>

      <div className="admin-card" style={{ maxWidth: 720, marginTop: "1rem" }}>
        <div className="admin-card-body" style={{ fontSize: "0.85rem", color: "var(--text-muted)", lineHeight: 1.7 }}>
          <p style={{ margin: "0 0 0.5rem", fontWeight: 600, color: "var(--text)" }}>工作原理</p>
          <ol style={{ margin: 0, paddingLeft: "1.2rem" }}>
            <li>检测当前可执行文件路径（<code>os.Executable()</code>）和运行平台（<code>GOOS/GOARCH</code>）</li>
            <li>调用 GitHub API 获取 <code>CrossDark/GoKYCH</code> 的 latest Release</li>
            <li>根据平台选择对应二进制（如 <code>gokych-linux-amd64</code>），下载到同目录临时文件</li>
            <li>用 Release 中的 <code>SHA256SUMS</code> 校验文件完整性</li>
            <li>备份当前二进制为 <code>.prev</code>，原子替换为新版本</li>
            <li>2 秒后优雅关闭 HTTP 服务并用 <code>syscall.Exec</code> 替换进程（PID 不变，systemd 无缝接管）</li>
          </ol>
          <p style={{ margin: "0.75rem 0 0" }}>
            ⚠️ 若更新后无法启动，可手动回滚：<code style={{
              background: "var(--surface-2)", padding: "0.1rem 0.4rem", borderRadius: 3,
            }}>cp gokych.prev gokych && systemctl restart gokych</code>
          </p>
        </div>
      </div>
    </div>
  );
}
