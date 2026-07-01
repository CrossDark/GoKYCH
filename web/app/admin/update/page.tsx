"use client";

import { useState, useEffect, useCallback, useRef, type ReactNode } from "react";
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
  can_write_error?: string;
  write_err_category?: "erofs" | "eacces" | "eperm" | "other" | string;
  process_user?: string;
  dir_permissions?: string;
  in_container?: boolean;
  mount_options?: string;
  published_at?: string;
  release_url?: string;
  release_notes?: string;
  download_size?: number;
  error?: string;
}

interface ApplyResult {
  success: boolean;
  message: string;
}

interface UpdateStatus {
  status: "idle" | "downloading" | "verifying" | "replacing" | "restarting" | "done" | "error";
  version?: string;
  message: string;
  error?: string;
  backup?: string;
  progress: number;
  total: number;
  elapsed_sec: number;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

function WriteErrorPanel({ info }: { info: UpdateCheckInfo }) {
  const cat = info.write_err_category || "other";
  const dir = info.binary_path ? info.binary_path.substring(0, info.binary_path.lastIndexOf("/")) : "";
  const binName = info.binary_path ? info.binary_path.substring(info.binary_path.lastIndexOf("/") + 1) : "gokych";

  let title = "写入失败";
  let diagnosis: React.ReactNode = null;
  let solutions: React.ReactNode[] = [];

  if (cat === "erofs") {
    title = "🔒 只读文件系统（read-only file system）";
    diagnosis = (
      <>
        <p>文件权限 <code>{info.dir_permissions || "0777"}</code> 没有问题——<strong>chmod / chown 无法解决这个问题</strong>。
        写入失败是因为该目录所在的文件系统被<strong>挂载为只读</strong>，操作系统从内核层面拒绝了所有写操作。</p>
        {info.mount_options && info.mount_options.includes("ro") && (
          <p>检测到挂载选项包含 <code>ro</code>（read-only）：<code>{info.mount_options}</code></p>
        )}
      </>
    );
    if (info.in_container) {
      solutions = [
        <>这是<strong>容器环境</strong>：二进制在镜像层中本身就是只读的。自更新功能不适用于容器部署——请通过重建/拉取新镜像来更新，或将二进制放在可写 volume 挂载路径下。</>,
      ];
    } else {
      solutions = [
        <>
          <strong>检查 systemd 服务配置</strong>（最常见原因）：运行 <code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>systemctl cat {binName}</code>，查看是否有 <code>ProtectSystem=strict</code>、<code>ProtectSystem=full</code>、<code>ReadOnlyPaths=-/opt</code>、<code>ReadOnlyPaths={dir}</code> 等指令。这些会将 <code>/opt</code> 以只读方式挂载到服务命名空间中。修复方法：在服务文件中添加 <code>ReadWritePaths={dir}</code> 然后 <code>systemctl daemon-reload && systemctl restart {binName}</code>。
        </>,
        <>
          <strong>检查 /etc/fstab</strong>：运行 <code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>mount | grep '{dir}'</code> 或 <code>findmnt {dir}</code> 查看挂载选项是否包含 <code>ro</code>。
        </>,
        <>
          <strong>检查内核日志</strong>：运行 <code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>dmesg | tail -30</code>，如果看到 "remounted read-only" 或 EXT4/XFS error，说明磁盘有 I/O 错误导致内核自动保护，先修复磁盘问题。
        </>,
      ];
    }
  } else if (cat === "eacces") {
    title = "🚫 权限不足（permission denied）";
    diagnosis = (
      <p>进程用户 <code>{info.process_user}</code> 对目录 <code>{dir}</code>（权限 <code>{info.dir_permissions}</code>）没有写入权限。</p>
    );
    solutions = [
      <>修改目录权限：<code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>chmod 775 {dir}</code></>,
      <>修改目录所有者：<code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>chown {info.process_user || "$USER"} {dir}</code></>,
      <>如使用 systemd：确保服务 <code>User=</code> 与目录所有者一致。</>,
    ];
  } else if (cat === "eperm") {
    title = "⛔ 操作被拒绝（operation not permitted）";
    diagnosis = (
      <p>操作系统安全模块拒绝了写入操作，这通常不是普通文件权限问题。</p>
    );
    solutions = [
      <>检查文件不可变标志：<code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>lsattr {dir}/{binName}</code>，如果有 <code>i</code> 标志（immutable），用 <code>chattr -i {dir}/{binName}</code> 解除。</>,
      <>检查 SELinux/AppArmor 状态：<code style={{background:"#fee2e2",padding:"0.1rem 0.3rem",borderRadius:3}}>getenforce</code> 或 <code>aa-status</code>。</>,
      <>macOS 上检查 SIP（系统完整性保护）：二进制路径如果在受 SIP 保护的目录（如 <code>/System</code>、<code>/usr</code>）下，即使是 root 也无法写入。</>,
    ];
  } else {
    diagnosis = <p>错误信息：<code>{info.can_write_error}</code></p>;
    solutions = [
      <>检查磁盘空间是否充足（<code>df -h {dir}</code>）。</>,
      <>检查目录是否存在且可访问（<code>ls -la {dir}</code>）。</>,
    ];
  }

  return (
    <div style={{
      marginTop: "0.5rem",
      padding: "0.65rem 0.85rem",
      background: "#fef2f2",
      border: "1px solid #fecaca",
      borderRadius: 6,
      fontSize: "0.82rem",
      color: "#991b1b",
      lineHeight: 1.7,
    }}>
      <div style={{ fontWeight: 700, fontSize: "0.85rem", marginBottom: "0.35rem" }}>{title}</div>
      {diagnosis}
      {info.can_write_error && cat !== "other" && (
        <div style={{ fontSize: "0.78rem", color: "#7f1d1d", marginTop: "0.25rem", fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" }}>
          系统错误: {info.can_write_error}
        </div>
      )}
      {solutions.length > 0 && (
        <div style={{ marginTop: "0.5rem" }}>
          <div style={{ fontWeight: 600, marginBottom: "0.25rem", color: "#7f1d1d" }}>🔧 解决方法：</div>
          <ul style={{ margin: 0, paddingLeft: "1.2rem" }}>
            {solutions.map((s, i) => <li key={i} style={{ margin: "0.25rem 0" }}>{s}</li>)}
          </ul>
        </div>
      )}
      {(info.process_user || info.dir_permissions) && (
        <div style={{ marginTop: "0.5rem", fontSize: "0.78rem", color: "#7f1d1d", borderTop: "1px solid #fecaca", paddingTop: "0.4rem" }}>
          {info.process_user && <>进程用户: <code>{info.process_user}</code>{"　"}</>}
          {info.dir_permissions && <>目录权限: <code>{info.dir_permissions}</code>{"　"}</>}
          {info.mount_options && <>挂载选项: <code>{info.mount_options}</code></>}
        </div>
      )}
    </div>
  );
}

function ReleaseNotesHtml({ markdown }: { markdown: string }) {
  const [html, setHtml] = useState<string>("");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [{ marked }, DOMPurify] = await Promise.all([
        import("marked"),
        import("dompurify").then(m => m.default),
      ]);
      if (cancelled) return;
      marked.setOptions({ gfm: true, breaks: false });
      const raw = marked.parse(markdown) as string;
      const safe = DOMPurify.sanitize(raw, {
        ALLOWED_TAGS: [
          "a", "b", "blockquote", "br", "code", "em", "h1", "h2", "h3",
          "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre",
          "s", "span", "strong", "sub", "sup", "table", "tbody", "td",
          "th", "thead", "tr", "u", "ul", "del", "details", "summary",
          "input", "div", "cite", "kbd", "mark", "small",
        ],
        ALLOWED_ATTR: [
          "href", "title", "alt", "src", "class", "target", "rel",
          "checked", "type", "disabled", "id", "align",
        ],
        ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[#/])/i,
      });
      if (!cancelled) setHtml(safe);
    })();
    return () => { cancelled = true; };
  }, [markdown]);

  if (!html) {
    return (
      <pre style={{
        background: "var(--surface-2)",
        padding: "0.75rem 1rem",
        borderRadius: 6,
        fontSize: "0.82rem",
        maxHeight: 300,
        overflow: "auto",
        whiteSpace: "pre-wrap",
        wordBreak: "break-word",
        margin: 0,
        fontFamily: "inherit",
        lineHeight: 1.5,
      }}>{markdown}</pre>
    );
  }

  return (
    <div
      className="release-notes-md"
      style={{
        background: "var(--surface-2)",
        padding: "0.75rem 1rem",
        borderRadius: 6,
        fontSize: "0.85rem",
        maxHeight: 400,
        overflow: "auto",
        lineHeight: 1.6,
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

// ProgressBar renders a download/processing indicator.
function ProgressBar({ status }: { status: UpdateStatus }) {
  const isDownload = status.status === "downloading" && status.total > 0;
  const pct = isDownload ? Math.min(100, Math.round((status.progress / status.total) * 100)) : 0;

  const statusIcon: Record<string, string> = {
    idle: "⏳",
    downloading: "⬇️",
    verifying: "🔍",
    replacing: "🔄",
    restarting: "🔁",
    done: "✅",
    error: "❌",
  };

  return (
    <div style={{
      marginTop: "0.75rem",
      padding: "0.75rem 1rem",
      background: status.status === "error" ? "#fef2f2" : "#eff6ff",
      border: "1px solid",
      borderColor: status.status === "error" ? "#fecaca" : "#bfdbfe",
      borderRadius: 6,
      fontSize: "0.85rem",
    }}>
      <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", marginBottom: isDownload ? "0.5rem" : 0 }}>
        <span style={{ fontSize: "1.1rem" }}>{statusIcon[status.status] || "⏳"}</span>
        <span style={{ fontWeight: 600 }}>{status.message}</span>
        {status.version && (
          <code style={{
            background: "rgba(0,0,0,0.06)",
            padding: "0.1rem 0.4rem",
            borderRadius: 3,
            fontSize: "0.8rem",
          }}>{status.version}</code>
        )}
        <span style={{ marginLeft: "auto", color: "var(--text-muted)", fontSize: "0.8rem" }}>
          {status.elapsed_sec.toFixed(0)}s
        </span>
      </div>
      {isDownload && (
        <>
          <div style={{
            width: "100%",
            height: 8,
            background: "rgba(0,0,0,0.08)",
            borderRadius: 4,
            overflow: "hidden",
          }}>
            <div style={{
              width: pct + "%",
              height: "100%",
              background: "linear-gradient(90deg, #3b82f6, #8b5cf6)",
              borderRadius: 4,
              transition: "width 0.25s ease",
            }} />
          </div>
          <div style={{
            display: "flex",
            justifyContent: "space-between",
            fontSize: "0.78rem",
            color: "var(--text-muted)",
            marginTop: "0.3rem",
            fontFamily: "ui-monospace, monospace",
          }}>
            <span>{formatBytes(status.progress)} / {formatBytes(status.total)}</span>
            <span>{pct}%</span>
          </div>
        </>
      )}
      {status.error && (
        <div style={{
          marginTop: "0.5rem",
          padding: "0.5rem 0.7rem",
          background: "#fee2e2",
          borderRadius: 4,
          color: "#991b1b",
          fontSize: "0.82rem",
          fontFamily: "ui-monospace, monospace",
        }}>
          {status.error}
        </div>
      )}
      {status.status === "done" && status.backup && (
        <div style={{ fontSize: "0.8rem", color: "#065f46", marginTop: "0.5rem" }}>
          旧版本已备份到: <code style={{ fontFamily: "ui-monospace, monospace" }}>{status.backup}</code>
        </div>
      )}
    </div>
  );
}

export default function AdminUpdate() {
  const toast = useToast();
  const [csrf, setCsrf] = useState("");
  const [info, setInfo] = useState<UpdateCheckInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [updStatus, setUpdStatus] = useState<UpdateStatus | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const pollStatus = useCallback(async () => {
    try {
      const res = await apiFetch(apiUrl("/api/admin/update/status"));
      if (res.ok) {
        const data: UpdateStatus = await res.json();
        setUpdStatus(data);

        // Terminal states: stop polling
        if (data.status === "done") {
          setApplying(false);
          toast.success(data.message);
          // Wait for restart, then reload
          setTimeout(() => window.location.reload(), 5000);
          return;
        }
        if (data.status === "error") {
          setApplying(false);
          toast.error(data.error || "更新失败");
          return;
        }
      }
    } catch {
      // ignore polling errors
    }
    // Continue polling
    pollTimerRef.current = setTimeout(pollStatus, 1000);
  }, [toast]);

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token)).catch(() => {});
    return () => stopPolling();
  }, [stopPolling]);

  const check = useCallback(async () => {
    setChecking(true);
    try {
      const res = await apiFetch(apiUrl("/api/admin/update/check"));
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      const data: UpdateCheckInfo = await res.json();
      setInfo(data);
      if (data.error) {
        toast.error("检查完成但有警告: " + data.error);
      } else if (data.update_available) {
        // found new version, no toast needed (UI shows it)
      } else {
        toast.success(`当前版本 ${data.current_version} 已是最新`);
      }
    } catch (e: any) {
      toast.error("检查更新失败: " + e.message);
    } finally {
      setChecking(false);
    }
  }, [toast]);

  // Auto-check on mount
  useEffect(() => {
    if (csrf) check();
  }, [csrf, check]);

  // Check for in-progress update on mount
  useEffect(() => {
    if (!csrf) return;
    apiFetch(apiUrl("/api/admin/update/status")).then(async (res) => {
      if (!res.ok) return;
      const data: UpdateStatus = await res.json();
      if (data.status !== "idle" && data.status !== "done" && data.status !== "error") {
        setApplying(true);
        setUpdStatus(data);
        pollTimerRef.current = setTimeout(pollStatus, 1000);
      } else if (data.status === "done" || data.status === "error") {
        setUpdStatus(data);
      }
    }).catch(() => {});
  }, [csrf, pollStatus]);

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
      // Start polling for progress
      setUpdStatus({
        status: "downloading",
        message: data.message,
        progress: 0,
        total: info.download_size || 0,
        elapsed_sec: 0,
      });
      pollTimerRef.current = setTimeout(pollStatus, 800);
    } catch (e: any) {
      setApplying(false);
      toast.error("更新失败: " + e.message);
    }
  }, [info, csrf, pollStatus, toast]);

  const isInProgress = applying && updStatus && updStatus.status !== "done" && updStatus.status !== "error";

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
                  {info.can_write ? "✓ 可写" : "✗ 不可写"}
                </span>
              )}
              {info?.in_container && (
                <span style={{
                  marginLeft: "0.5rem",
                  fontSize: "0.75rem",
                  padding: "0.1rem 0.4rem",
                  borderRadius: 3,
                  background: "#fef3c7",
                  color: "#92400e",
                }}>容器环境</span>
              )}
              {info && !info.can_write && (
                <WriteErrorPanel info={info} />
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
          {info?.release_notes && info.update_available && !isInProgress && (
            <div>
              <div style={{ fontWeight: 600, marginBottom: "0.4rem", fontSize: "0.9rem" }}>
                Release Notes
              </div>
              <ReleaseNotesHtml markdown={info.release_notes} />
              {info.release_url && (
                <a href={info.release_url} target="_blank" rel="noopener noreferrer"
                   style={{ fontSize: "0.8rem", color: "var(--accent)" }}>
                  在 GitHub 查看 →
                </a>
              )}
            </div>
          )}

          {/* Progress / status panel during update */}
          {updStatus && (updStatus.status !== "idle" || applying) && (
            <ProgressBar status={updStatus} />
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
        </div>
      </div>

      <div className="admin-card" style={{ maxWidth: 720, marginTop: "1rem" }}>
        <div className="admin-card-body" style={{ fontSize: "0.85rem", color: "var(--text-muted)", lineHeight: 1.7 }}>
          <p style={{ margin: "0 0 0.5rem", fontWeight: 600, color: "var(--text)" }}>工作原理</p>
          <ol style={{ margin: 0, paddingLeft: "1.2rem" }}>
            <li>检测当前可执行文件路径（<code>os.Executable()</code>）和运行平台（<code>GOOS/GOARCH</code>）</li>
            <li>调用 GitHub API 获取 <code>CrossDark/GoKYCH</code> 的 latest Release</li>
            <li>后台异步下载对应平台二进制（显示进度），不会阻塞浏览器</li>
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
