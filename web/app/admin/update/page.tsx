"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import { getCsrf, getUpdateStatus, checkUpdate, applyUpdate, setUpdateSource } from "@/lib/api";
import type { UpdateCheckInfo, UpdateSource, UpdateStatus } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { fmtDateTime } from "@/lib/format";
import { UpdateWriteErrorPanel } from "@/components/admin/UpdateWriteErrorPanel";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
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

// SourceButton — pill-style toggle for selecting the update source.
// Highlights the active source and shows a spinner while the switch
// request is in flight.
function SourceButton({
  source,
  label,
  current,
  switching,
  disabled,
  onClick,
}: {
  source: UpdateSource;
  label: string;
  current?: UpdateSource;
  switching: UpdateSource | null;
  disabled: boolean;
  onClick: (s: UpdateSource) => void;
}) {
  const isActive = current === source;
  const isSwitching = switching === source;
  return (
    <button
      type="button"
      onClick={() => onClick(source)}
      disabled={disabled || isActive || isSwitching}
      style={{
        padding: "0.35rem 0.85rem",
        fontSize: "0.85rem",
        borderRadius: 999,
        border: "1px solid",
        borderColor: isActive ? "var(--accent)" : "var(--border)",
        background: isActive ? "var(--accent)" : "transparent",
        color: isActive ? "#fff" : "var(--text)",
        cursor: isActive ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
        transition: "all 0.15s",
      }}
    >
      {isSwitching ? "切换中…" : label}
    </button>
  );
}

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
  const [switchingSource, setSwitchingSource] = useState<UpdateSource | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Silences the "已是最新" / "可更新" toast on the auto check that runs
  // when the page first mounts — the UI already shows the status inline
  // in the cards below, and re-issuing the toast on every render loop
  // (when an upstream `toast` reference is unstable) used to flood the
  // screen with "v0.3.20 已是最新" the whole time the page was open.
  // Manually clicking "🔄 检查更新" (or applying an update) sets this
  // ref to true so the same call surface still surfaces feedback.
  const allowCheckToastRef = useRef(false);
  const lastCheckedVersionRef = useRef<string | null>(null);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const pollStatus = useCallback(() => {
    const doPoll = async () => {
      try {
        const data = await getUpdateStatus();
        setUpdStatus(data);

        if (data.status === "done") {
          setApplying(false);
          toast.success(data.message);
          setTimeout(() => window.location.reload(), 5000);
          return;
        }
        if (data.status === "error") {
          setApplying(false);
          toast.error(data.error || "更新失败");
          return;
        }
      } catch {
      }
      pollTimerRef.current = setTimeout(doPoll, 1000);
    };
    doPoll();
  }, [toast]);

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token)).catch(() => {});
    return () => stopPolling();
  }, [stopPolling]);

  const check = useCallback(async () => {
    setChecking(true);
    try {
      const data = await checkUpdate();
      setInfo(data);
      if (data.error) {
        if (allowCheckToastRef.current) toast.error("检查完成但有警告: " + data.error);
      } else if (data.update_available) {
        if (allowCheckToastRef.current) toast.success(`发现新版本 ${data.latest_version}`);
      } else {
        // De-dupe: only surface the "up-to-date" toast once per (version,
        // latest_version) pair. With the version comparison fixed on the
        // server, the same pair won't change while the page is open —
        // so if we already toasted it, don't toast again.
        const tag = `${data.current_version}|${data.latest_version ?? ""}`;
        if (allowCheckToastRef.current && lastCheckedVersionRef.current !== tag) {
          toast.success(`当前版本 ${data.current_version} 已是最新`);
        }
        lastCheckedVersionRef.current = tag;
      }
    } catch (e: any) {
      if (allowCheckToastRef.current) toast.error("检查更新失败: " + e.message);
    } finally {
      allowCheckToastRef.current = false;
      setChecking(false);
    }
  }, [toast]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (csrf) check();
  }, [csrf, check]);

  useEffect(() => {
    if (!csrf) return;
    getUpdateStatus().then((data) => {
      if (data.status !== "idle" && data.status !== "done" && data.status !== "error") {
        setApplying(true);
        setUpdStatus(data);
        pollTimerRef.current = setTimeout(pollStatus, 1000);
      } else if (data.status === "done" || data.status === "error") {
        setUpdStatus(data);
      }
    }).catch(() => {});
  }, [csrf, pollStatus]);

  const handleApply = useCallback(async () => {
    allowCheckToastRef.current = true; // apply-implicit "I poked you" signal
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
      const data = await applyUpdate(csrf);
      if (!data.success) {
        throw new Error(data.error || data.message || "更新失败");
      }
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

  // Switch update source (github ↔ gitcode). Persisted server-side via
  // /admin/update/source. After a successful switch we re-check
  // immediately so the user sees the new source's release info without
  // a separate click.
  const handleSwitchSource = useCallback(async (next: UpdateSource) => {
    if (!csrf) return;
    if (info?.source === next) return;
    setSwitchingSource(next);
    try {
      const r = await setUpdateSource(csrf, next);
      toast.success(r.message || `已切换到 ${next}`);
      // Trigger a fresh check against the new source. The user explicitly
      // asked for this check via the source switch, so the version
      // comparison toast SHOULD fire even if the result is "up to date".
      allowCheckToastRef.current = true;
      const data = await checkUpdate();
      setInfo(data);
      if (data.error) {
        toast.error("检查完成但有警告: " + data.error);
      }
      setChecking(false);
    } catch (e: any) {
      toast.error("切换更新源失败: " + e.message);
      setChecking(false);
    } finally {
      setSwitchingSource(null);
    }
  }, [csrf, info?.source, toast, check]);

  const isInProgress = applying && updStatus && updStatus.status !== "done" && updStatus.status !== "error";

  return (
    <div className="admin-page">
      <h1 className="admin-page-title">系统更新</h1>
      <p className="admin-page-desc">
        从 Release 源自动检测最新版本（默认 GitHub，可切到 GitCode 国内镜像），下载匹配当前平台的二进制并热重启服务。仅站点所有者可执行。
      </p>

      <div className="admin-card" style={{ maxWidth: 720 }}>
        <div className="admin-card-body" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
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

            {/* Update source selector — github (default) or gitcode mirror.
                Persisted server-side via /admin/update/source. Disabled
                during a download / apply to avoid swapping underneath an
                in-flight job. */}
            <div style={{ color: "var(--text-muted)" }}>更新源</div>
            <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap", alignItems: "center" }}>
              <SourceButton
                source="github"
                label="GitHub"
                current={info?.source}
                switching={switchingSource}
                disabled={!!checking || !!applying}
                onClick={handleSwitchSource}
              />
              <SourceButton
                source="gitcode"
                label="GitCode (国内镜像)"
                current={info?.source}
                switching={switchingSource}
                disabled={!!checking || !!applying}
                onClick={handleSwitchSource}
              />
              {info?.source && (
                <span style={{ color: "var(--text-muted)", fontSize: "0.78rem" }}>
                  当前: <code style={{ background: "var(--surface-2)", padding: "0.05rem 0.3rem", borderRadius: 3 }}>{info.source}</code>
                </span>
              )}
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
                <UpdateWriteErrorPanel info={info} />
              )}
            </div>

            {info?.published_at && (
              <>
                <div style={{ color: "var(--text-muted)" }}>发布时间</div>
                <div>{fmtDateTime(info.published_at)}</div>
              </>
            )}

            {info?.download_size ? (
              <>
                <div style={{ color: "var(--text-muted)" }}>下载大小</div>
                <div>{formatBytes(info.download_size)}</div>
              </>
            ) : null}
          </div>

          {info?.release_notes && info.update_available && !isInProgress && (
            <div>
              <div style={{ fontWeight: 600, marginBottom: "0.4rem", fontSize: "0.9rem" }}>
                Release Notes
              </div>
              <ReleaseNotesHtml markdown={info.release_notes} />
              {info.release_url && (
                <a href={info.release_url} target="_blank" rel="noopener noreferrer"
                   style={{ fontSize: "0.8rem", color: "var(--accent)" }}>
                  {info.source === "gitcode" ? "在 GitCode 查看 →" : "在 GitHub 查看 →"}
                </a>
              )}
            </div>
          )}

          {updStatus && (updStatus.status !== "idle" || applying) && (
            <ProgressBar status={updStatus} />
          )}

          <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap" }}>
            <button
              className="btn"
              onClick={() => {
                // Manual click: enable the toast feedback for this check.
                allowCheckToastRef.current = true;
                void check();
              }}
              disabled={checking || applying}
            >
              {checking ? "检查中…" : "🔄 检查更新"}
            </button>
            <button
              className="btn btn-primary"
              onClick={handleApply}
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
            <li>调用所选源的 API(GitHub <code>api.github.com/repos/CrossDark/GoKYCH</code> 或 GitCode <code>api/v5/repos/CrossDark/GoKych</code>)获取 latest Release</li>
            <li>后台异步下载对应平台二进制（显示进度），不会阻塞浏览器</li>
            <li>用 Release 中的 <code>SHA256SUMS</code> 校验文件完整性</li>
            <li>备份当前二进制为 <code>.prev</code>，原子替换为新版本</li>
            <li>若是 systemd 部署（<code>INVOCATION_ID</code> 存在 + 有 <code>sudo systemctl</code> 权限），spawn 一次 <code>sudo systemctl restart gokych.service</code>，让 systemd 走 SIGTERM → 优雅停 → 拉起新版本；非 systemd 环境 fallback 到 <code>syscall.Exec</code>，PID 不变</li>
          </ol>
          <p style={{ margin: "0.75rem 0 0" }}>
            ⚠️ 若更新后无法启动，可手动回滚：<code style={{
              background: "var(--surface-2)", padding: "0.1rem 0.4rem", borderRadius: 3,
            }}>cp gokych.prev gokych && sudo systemctl restart gokych</code>
          </p>
        </div>
      </div>
    </div>
  );
}