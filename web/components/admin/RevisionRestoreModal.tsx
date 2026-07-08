"use client";

import { useState, useEffect, useRef } from "react";
import { AdminModal } from "@/components/admin/AdminModal";
import { restoreRevision } from "@/lib/api";
import type { Article } from "@/lib/types";

/**
 * RevisionRestoreModal — the "回滚" confirmation dialog.
 *
 * Why a modal (not a toast / inline confirm)
 * ───────────────────────────────────────────
 * Restore is a content-altering action. A user clicking "回滚" on a
 * year-old revision needs to know:
 *   1. WHICH version they're rolling back to (#N).
 *   2. That a new revision row will be created (chain stays append-only).
 *   3. Optionally, why — the message field, which becomes the audit
 *      trail label on the new row.
 * A single-line toast (V5a's placeholder) can't carry that. A modal
 * also gives us a natural place to surface the "no-op fast path" —
 * if the user clicks restore on the current version, the server
 * returns success without writing a row, and we tell the user
 * "已回滚(无新版本,因为目标与当前一致)" rather than silently succeeding.
 *
 * The message field defaults to empty — content.RestoreRevisionCtx
 * fills in "restore from #N" if we send ""; we surface this in the
 * placeholder so the user knows the field is optional.
 *
 * Success path
 * ────────────
 * On 200 OK, the server returns {article, restored_to: <seq>}. We
 * invoke `onRestored(article)` and close. The parent (article editor
 * page) re-syncs its `form` / `initial` state to the restored
 * article, clears `isDirty`, and bumps `revisionAction` to null.
 * The user sees the editor immediately reflect the rollback; the
 * revision list (if they reopen the drawer) shows a new top row.
 */

interface RevisionRestoreModalProps {
  /** The seq to restore to; `null` = modal closed. */
  seq: number | null;
  type: string;
  slug: string;
  csrf: string;
  onClose: () => void;
  /**
   * Called with the server's returned `article` on success. The
   * parent uses this to re-sync the editor's form state to the
   * restored content (otherwise the editor would still show the
   * pre-restore text and the user would have to manually refresh
   * the page).
   */
  onRestored: (article: Article) => void;
}

export function RevisionRestoreModal({
  seq,
  type,
  slug,
  csrf,
  onClose,
  onRestored,
}: RevisionRestoreModalProps) {
  const [message, setMessage] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Tracks which seq we last submitted for, so a stale resolve
  // (e.g. user closed the modal mid-request) doesn't fire
  // onRestored on an already-dismissed flow.
  const submittedFor = useRef<number | null>(null);

  // Reset internal state when the modal opens/closes or the target
  // seq changes. Without this, opening the modal a second time on
  // a different seq would show the previous message + error.
  useEffect(() => {
    if (seq === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setMessage("");
       
      setSubmitting(false);
       
      setError(null);
      submittedFor.current = null;
    }
  }, [seq]);

  const open = seq !== null;

  const handleConfirm = async () => {
    if (seq === null) return;
    setSubmitting(true);
    setError(null);
    submittedFor.current = seq;
    try {
      const res = await restoreRevision(type, slug, seq, csrf, message.trim());
      if (submittedFor.current !== seq) {
        // The modal was closed (or target changed) while we were
        // waiting — don't fire onRestored on a stale flow.
        return;
      }
      onRestored(res.article);
      onClose();
    } catch (e: unknown) {
      if (submittedFor.current !== seq) return;
      setError((e as Error)?.message || "回滚失败。");
    } finally {
      if (submittedFor.current === seq) {
        setSubmitting(false);
      }
    }
  };

  return (
    <AdminModal
      open={open}
      onClose={onClose}
      title={seq === null ? "" : `回滚到 #${seq}?`}
      size="md"
      persistent={submitting}
    >
      <div>
        <p style={{ marginTop: 0, color: "var(--text-muted)" }}>
          回滚会创建一个新的历史版本(链 append-only,不会删除中间任何记录)。
          当前的最新内容会被替换为 <code>#{seq}</code> 的内容。
        </p>
        <div className="admin-form-group" style={{ marginBottom: 0 }}>
          <label
            htmlFor="restore-message"
            style={{ display: "block", marginBottom: 6 }}
          >
            提交说明
            <span
              style={{
                fontWeight: 400,
                color: "var(--text-muted)",
                marginLeft: 6,
              }}
            >
              (可选,留空则用默认 “restore from #{seq}”)
            </span>
          </label>
          <input
            id="restore-message"
            type="text"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder={`restore from #${seq ?? ""}`}
            disabled={submitting}
            maxLength={500}
            data-testid="restore-message-input"
            style={{ width: "100%" }}
          />
        </div>
        {error && (
          <div
            className="admin-notice admin-notice-error"
            style={{ marginTop: 12 }}
            data-testid="restore-error"
          >
            <span className="admin-notice-icon">✕</span>
            <div className="admin-notice-content">{error}</div>
          </div>
        )}
        <div
          className="admin-form-actions"
          style={{ marginTop: 16, marginBottom: 0 }}
        >
          <button
            type="button"
            className="admin-btn admin-btn-primary"
            onClick={handleConfirm}
            disabled={submitting}
            data-testid="restore-confirm"
          >
            {submitting ? "回滚中…" : `回滚到 #${seq ?? ""}`}
          </button>
          <button
            type="button"
            className="admin-btn admin-btn-ghost"
            onClick={onClose}
            disabled={submitting}
          >
            取消
          </button>
        </div>
      </div>
    </AdminModal>
  );
}
