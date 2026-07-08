"use client";

import { useEffect, useState, useRef } from "react";
import { AdminModal } from "@/components/admin/AdminModal";
import { getRevision, getRevisionDiff } from "@/lib/api";
import type { RevisionAction } from "@/components/admin/RevisionDrawer";
import type { RevisionDetail, RevisionDiffResult } from "@/lib/types";

/**
 * RevisionDiffModal — the "view single version" + "compare two versions"
 * half of the revision history UI.
 *
 * Two modes driven by the RevisionAction kind from the drawer:
 *   - kind === "view"     → fetch /revisions/{seq}, show full content
 *                            in a read-only <pre> pane.
 *   - kind === "compare"  → fetch /revisions/diff?from=seq&to=current,
 *                            render unified diff with + / − / context
 *                            line colouring.
 *
 * The fetch is triggered on open AND when the seq changes. A null
 * `action` prop is the "modal closed" state.
 *
 * Why a single component, not two
 * ───────────────────────────────
 * The two modes share 80% of their shell: same modal chrome, same
 * header layout, same error/loading states. Splitting them would
 * duplicate the AdminModal wrapper and the load lifecycle. The
 * only divergence is the body rendering, which is small enough
 * to keep inline.
 */

interface RevisionDiffModalProps {
  /** Which revision the user clicked. `null` = modal closed. */
  action: Extract<RevisionAction, { kind: "view" | "compare" }> | null;
  type: string;
  slug: string;
  /**
   * The current (latest) revision seq — used as `to` in compare mode
   * so the diff is against the live state. `null` if unknown (the
   * parent hasn't fetched the list yet); in that case the modal
   * diffs against the clicked seq itself, producing an empty diff
   * if the user clicked the current version, or a partial diff if
   * they clicked an older one. The parent should pass the value
   * from the drawer's `RevisionListResult.total`.
   */
  currentSeq: number | null;
  onClose: () => void;
}

export function RevisionDiffModal({
  action,
  type,
  slug,
  currentSeq,
  onClose,
}: RevisionDiffModalProps) {
  const [viewState, setViewState] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; data: RevisionDetail }
    | { kind: "error"; message: string }
  >({ kind: "idle" });
  const [diffState, setDiffState] = useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; data: RevisionDiffResult }
    | { kind: "error"; message: string }
  >({ kind: "idle" });

  // Track the seq we last fetched for, so a stale fetch resolving
  // after the modal has been closed/reopened on a different row
  // doesn't pollute the new state. Same pattern as a request
  // cancellation token but without the cancel complexity.
  const fetchedFor = useRef<{ kind: string; seq: number } | null>(null);

  useEffect(() => {
    if (!action) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setViewState({ kind: "idle" });
       
      setDiffState({ kind: "idle" });
      fetchedFor.current = null;
      return;
    }

    if (action.kind === "view") {
      setViewState({ kind: "loading" });
      fetchedFor.current = { kind: "view", seq: action.seq };
      getRevision(type, slug, action.seq)
        .then((data) => {
          if (
            fetchedFor.current?.kind === "view" &&
            fetchedFor.current.seq === action.seq
          ) {
            setViewState({ kind: "ok", data });
          }
        })
        .catch((e: any) => {
          if (
            fetchedFor.current?.kind === "view" &&
            fetchedFor.current.seq === action.seq
          ) {
            setViewState({
              kind: "error",
              message: e?.message || "加载版本内容失败。",
            });
          }
        });
    } else {
      // compare: diff against the current state. `to` defaults to
      // the clicked seq if the parent hasn't threaded currentSeq
      // through — that produces an empty diff for "compare
      // current ↔ current" and a partial diff for older seqs.
      // The parent should pass currentSeq from the drawer's
      // list-response `total` so users always see a clean
      // (seq ↔ current) comparison.
      setDiffState({ kind: "loading" });
      fetchedFor.current = { kind: "compare", seq: action.seq };
      const to = currentSeq ?? action.seq;
      getRevisionDiff(type, slug, action.seq, to)
        .then((data) => {
          if (
            fetchedFor.current?.kind === "compare" &&
            fetchedFor.current.seq === action.seq
          ) {
            setDiffState({ kind: "ok", data });
          }
        })
        .catch((e: any) => {
          if (
            fetchedFor.current?.kind === "compare" &&
            fetchedFor.current.seq === action.seq
          ) {
            setDiffState({
              kind: "error",
              message: e?.message || "加载 diff 失败。",
            });
          }
        });
    }
  }, [action, type, slug, currentSeq]);

  const open = action !== null;
  const title =
    action === null
      ? ""
      : action.kind === "view"
        ? `查看 #${action.seq}`
        : `对比 #${action.seq} ↔ 当前`;

  return (
    <AdminModal open={open} onClose={onClose} title={title} size="lg">
      {action?.kind === "view" ? (
        <ViewBody state={viewState} />
      ) : action?.kind === "compare" ? (
        <DiffBody state={diffState} />
      ) : null}
    </AdminModal>
  );
}

/**
 * Optional override for the "to" seq in compare mode. When the parent
 * knows the current revision seq (e.g. from the drawer's `total`), it
 * can pass it here so the diff is against the live state. Falls back
 * to the clicked seq (which produces an empty diff — a clear "you're
 * looking at the current version" signal).
 *
 * The current article editor wires this via:
  *   currentSeq={revisionListData?.total}
  * but V5b doesn't yet thread that through (it's a single prop on
  * the modal — see V5b's follow-up note).
  */
function ViewBody({
  state,
}: {
  state:
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; data: RevisionDetail }
    | { kind: "error"; message: string };
}) {
  if (state.kind === "idle" || state.kind === "loading") {
    return <p className="admin-empty">加载中…</p>;
  }
  if (state.kind === "error") {
    return <div className="admin-notice admin-notice-error">{state.message}</div>;
  }
  return (
    <div>
      <div
        style={{
          fontSize: "0.85em",
          color: "var(--text-muted)",
          marginBottom: 8,
        }}
      >
        <strong>当时标题:</strong> {state.data.revision.title}
        <span style={{ marginLeft: 12 }}>
          <strong>保存时间:</strong> {state.data.revision.created_at}
        </span>
        {state.data.revision.message && (
          <span
            style={{
              marginLeft: 12,
              fontStyle: "italic",
            }}
          >
            “{state.data.revision.message}”
          </span>
        )}
      </div>
      <pre
        data-testid="revision-view-content"
        style={{
          margin: 0,
          padding: 12,
          background: "var(--admin-bg, #f8fafc)",
          border: "1px solid var(--admin-border, #e5e7eb)",
          borderRadius: 4,
          maxHeight: "60vh",
          overflow: "auto",
          fontSize: "0.85em",
          lineHeight: 1.5,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
        }}
      >
        {state.data.content}
      </pre>
    </div>
  );
}

function DiffBody({
  state,
}: {
  state:
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "ok"; data: RevisionDiffResult }
    | { kind: "error"; message: string };
}) {
  if (state.kind === "idle" || state.kind === "loading") {
    return <p className="admin-empty">加载 diff 中…</p>;
  }
  if (state.kind === "error") {
    return <div className="admin-notice admin-notice-error">{state.message}</div>;
  }
  if (!state.data.diff) {
    return (
      <div className="admin-notice admin-notice-info">
        此版本与当前内容完全一致，没有可显示的 diff。
      </div>
    );
  }
  return (
    <div>
      <div
        style={{
          fontSize: "0.85em",
          color: "var(--text-muted)",
          marginBottom: 8,
        }}
      >
        <strong>From:</strong> #{state.data.from} — {state.data.from_title}
        <br />
        <strong>To:</strong>&nbsp;&nbsp;&nbsp; #{state.data.to} — {state.data.to_title}
      </div>
      <pre
        data-testid="revision-diff-content"
        style={{
          margin: 0,
          padding: 12,
          background: "var(--admin-bg, #f8fafc)",
          border: "1px solid var(--admin-border, #e5e7eb)",
          borderRadius: 4,
          maxHeight: "60vh",
          overflow: "auto",
          fontSize: "0.85em",
          lineHeight: 1.5,
          fontFamily:
            "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
        }}
      >
        {state.data.diff.split("\n").map((line, i) => (
          <DiffLine key={i} line={line} />
        ))}
      </pre>
    </div>
  );
}

function DiffLine({ line }: { line: string }) {
  // Unified-diff line prefixes: " " (context), "-" (deleted), "+"
  // (inserted). We colour them; everything else (hunk headers,
  // "No newline" markers) renders as plain text.
  let bg = "transparent";
  let color: string | undefined;
  if (line.startsWith("+")) {
    bg = "rgba(34, 197, 94, 0.12)";
    color = "#166534";
  } else if (line.startsWith("-")) {
    bg = "rgba(239, 68, 68, 0.12)";
    color = "#991b1b";
  } else if (line.startsWith("@@")) {
    bg = "rgba(59, 130, 246, 0.08)";
    color = "#1e40af";
  }
  return (
    <div
      style={{
        background: bg,
        color,
        padding: "0 4px",
        whiteSpace: "pre",
      }}
    >
      {line || "\u00a0"}
    </div>
  );
}
