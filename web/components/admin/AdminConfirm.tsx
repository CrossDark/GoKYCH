"use client";

import { useState, useCallback } from "react";
import { AdminModal } from "./AdminModal";

interface AdminConfirmProps {
  open: boolean;
  title: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  variant?: "danger" | "primary";
  onConfirm: () => void | Promise<void>;
  onCancel: () => void;
}

/**
 * Replaces native `window.confirm()` with the admin design system modal.
 * Shows a loading spinner on the confirm button while `onConfirm` is running.
 */
export function AdminConfirm({
  open,
  title,
  message,
  confirmText = "确定",
  cancelText = "取消",
  variant = "danger",
  onConfirm,
  onCancel,
}: AdminConfirmProps) {
  const [busy, setBusy] = useState(false);

  const handleConfirm = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    try {
      await onConfirm();
      setBusy(false);
    } catch {
      setBusy(false);
      // Caller is responsible for showing error toast; rethrow or just keep modal open.
    }
  }, [busy, onConfirm]);

  return (
    <AdminModal
      open={open}
      onClose={onCancel}
      title={title}
      size="sm"
      persistent={busy}
      footer={
        <>
          <button
            type="button"
            className="admin-btn admin-btn-ghost"
            onClick={onCancel}
            disabled={busy}
          >
            {cancelText}
          </button>
          <button
            type="button"
            className={`admin-btn admin-btn-${variant} ${busy ? "admin-btn-loading" : ""}`}
            onClick={handleConfirm}
            disabled={busy}
          >
            {confirmText}
          </button>
        </>
      }
    >
      <p style={{ margin: 0, color: "var(--text-primary)", lineHeight: 1.6 }}>
        {message}
      </p>
    </AdminModal>
  );
}
