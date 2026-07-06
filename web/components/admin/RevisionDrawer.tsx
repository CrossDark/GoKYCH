"use client";

import { useEffect, useState, useCallback } from "react";
import { listRevisions } from "@/lib/api";
import type { RevisionListItem, RevisionListResult } from "@/lib/types";
import { fmtDateTimeShort } from "@/lib/format";

/**
 * RevisionDrawer — the right-rail "📜 历史" panel for the article editor.
 *
 * Opens from the editor's `📜 历史` button. Lazily fetches
 * GET /api/articles/{type}/{slug}/revisions on first open (no caching —
 * history is short and the user typically wants a fresh view after a
 * save). Renders one row per revision with three action buttons per
 * row: 查看 / 对比当前 / 回滚. The actual viewing / diff / restore UX
 * is owned by sibling modal components (V5b/V5c) — this drawer is the
 * "list + action dispatcher" shell.
 *
 * State ownership: the parent page holds the modal state (which modal
 * is open, for which seq, with what data). This component just emits
 * callbacks; it does not know how a diff or restore is presented. That
 * keeps the drawer testable in isolation and lets V5b/V5c swap modal
 * implementations without touching this file.
 */

export type RevisionAction =
  | { kind: "view"; seq: number }
  | { kind: "compare"; seq: number }
  | { kind: "restore"; seq: number };

interface RevisionDrawerProps {
  open: boolean;
  onClose: () => void;
  type: string;
  slug: string;
  /** Dispatched when the user clicks 查看 / 对比当前 / 回滚 on a row. */
  onAction: (action: RevisionAction) => void;
  /**
   * Called once per successful fetch with the list's `total` — i.e.
   * the current (latest) revision seq. The parent uses this to
   * populate the "compare against current" modal's `to` parameter
   * so a click on row #N diffs against the live state, not against
   * #N itself.
   */
  onLoaded?: (currentSeq: number) => void;
}

export function RevisionDrawer({
  open,
  onClose,
  type,
  slug,
  onAction,
  onLoaded,
}: RevisionDrawerProps) {
  const [state, setState] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; data: RevisionListResult }
    | { kind: "error"; message: string }
  >({ kind: "idle" });

  const fetchRevisions = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const data = await listRevisions(type, slug, 1, 100);
      setState({ kind: "ok", data });
      if (onLoaded && data.total > 0) {
        // The list endpoint already gives us `total` (the article's
        // current seq), so we don't need a second fetch to learn
        // the latest seq. Fire onLoaded only on success so the
        // parent's `currentSeq` doesn't get reset to 0 on a
        // transient fetch failure.
        onLoaded(data.total);
      }
    } catch (e: any) {
      setState({
        kind: "error",
        message: e?.message || "加载历史版本失败。",
      });
    }
  }, [type, slug, onLoaded]);

  // Lazy fetch: only when the drawer is actually opened. Re-fetch
  // when type/slug change (the editor can navigate between articles
  // without unmounting the page).
  useEffect(() => {
    if (!open) return;
    void fetchRevisions();
  }, [open, fetchRevisions]);

  // ESC closes the drawer. Mirrors AdminModal's behaviour.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <>
      <div
        className={`side-drawer-overlay ${open ? "open" : ""}`}
        onClick={onClose}
        aria-hidden="true"
      />
      <aside
        className={`side-drawer ${open ? "open" : ""}`}
        aria-hidden={!open}
        aria-label="文章历史版本"
        data-testid="revision-drawer"
      >
        <div className="side-drawer-header">
          <h3>📜 历史版本</h3>
          <button
            type="button"
            className="side-drawer-close"
            onClick={onClose}
            aria-label="关闭"
            title="关闭"
          >
            ×
          </button>
        </div>
        <div className="side-drawer-body">
          {state.kind === "loading" && (
            <p className="side-drawer-empty">加载中…</p>
          )}
          {state.kind === "error" && (
            <div className="side-drawer-empty">
              <p style={{ color: "var(--admin-danger)" }}>{state.message}</p>
              <button
                type="button"
                className="admin-btn admin-btn-outline admin-btn-sm"
                onClick={() => void fetchRevisions()}
                style={{ marginTop: 8 }}
              >
                重试
              </button>
            </div>
          )}
          {state.kind === "ok" && state.data.revisions.length === 0 && (
            <p className="side-drawer-empty">暂无历史版本</p>
          )}
          {state.kind === "ok" && state.data.revisions.length > 0 && (
            <RevisionList
              revisions={state.data.revisions}
              onAction={onAction}
            />
          )}
        </div>
      </aside>
    </>
  );
}

function RevisionList({
  revisions,
  onAction,
}: {
  revisions: RevisionListItem[];
  onAction: (action: RevisionAction) => void;
}) {
  return (
    <ul
      style={{
        listStyle: "none",
        margin: 0,
        padding: 0,
        display: "flex",
        flexDirection: "column",
      }}
    >
      {revisions.map((r) => (
        <li
          key={r.id}
          data-testid={`revision-row-${r.seq}`}
          style={{
            borderBottom: "1px solid var(--admin-border, #e5e7eb)",
            padding: "10px 0",
            display: "flex",
            flexDirection: "column",
            gap: 6,
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: 6,
              fontSize: "0.85em",
            }}
          >
            <span
              style={{
                fontWeight: 600,
                color: "var(--admin-fg, #111827)",
              }}
            >
              #{r.seq}
            </span>
            {r.is_snapshot && (
              <span
                className="admin-badge admin-badge-neutral"
                style={{ fontSize: "0.7em" }}
                title="完整快照（chain 起点或每 50 个版本一次）"
              >
                snapshot
              </span>
            )}
            <span
              style={{
                color: "var(--text-muted)",
                fontSize: "0.85em",
                marginLeft: "auto",
              }}
              title={r.created_at}
            >
              {fmtDateTimeShort(r.created_at)}
            </span>
          </div>
          {r.message && (
            <div
              style={{
                fontSize: "0.85em",
                color: "var(--text-muted)",
                fontStyle: "italic",
                wordBreak: "break-word",
              }}
            >
              {r.message}
            </div>
          )}
          <div
            style={{
              display: "flex",
              gap: 4,
              flexWrap: "wrap",
              marginTop: 2,
            }}
          >
            <button
              type="button"
              className="admin-btn admin-btn-ghost admin-btn-sm"
              onClick={() => onAction({ kind: "view", seq: r.seq })}
              data-testid={`revision-view-${r.seq}`}
            >
              查看
            </button>
            <button
              type="button"
              className="admin-btn admin-btn-ghost admin-btn-sm"
              onClick={() => onAction({ kind: "compare", seq: r.seq })}
              data-testid={`revision-compare-${r.seq}`}
            >
              对比当前
            </button>
            <button
              type="button"
              className="admin-btn admin-btn-outline admin-btn-sm"
              onClick={() => onAction({ kind: "restore", seq: r.seq })}
              data-testid={`revision-restore-${r.seq}`}
              title="回滚到此版本（会创建一条新历史记录）"
            >
              回滚
            </button>
          </div>
        </li>
      ))}
    </ul>
  );
}
