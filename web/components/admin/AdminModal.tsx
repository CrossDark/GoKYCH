"use client";

import { useEffect, useRef, useCallback, ReactNode } from "react";

interface AdminModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  size?: "sm" | "md" | "lg";
  children: ReactNode;
  footer?: ReactNode;
  /** Set true to disable closing on backdrop click / Escape (e.g. busy state). */
  persistent?: boolean;
}

/**
 * Standard admin modal dialog. Provides:
 * - Backdrop click + Escape to close (unless `persistent`)
 * - Body scroll lock via `body.admin-modal-open`
 * - Auto-focus the first focusable element
 * - Roving focus trap within the dialog
 */
export function AdminModal({
  open,
  onClose,
  title,
  size = "md",
  children,
  footer,
  persistent = false,
}: AdminModalProps) {
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);

  const close = useCallback(() => {
    if (persistent) return;
    onClose();
  }, [persistent, onClose]);

  // Body scroll lock + focus management
  useEffect(() => {
    if (!open) return;
    previouslyFocused.current = document.activeElement as HTMLElement | null;
    document.body.classList.add("admin-modal-open");
    // Focus the dialog (or the first focusable inside)
    requestAnimationFrame(() => {
      const dlg = dialogRef.current;
      if (!dlg) return;
      const focusable = dlg.querySelector<HTMLElement>(
        "input, select, textarea, button, [href], [tabindex]:not([tabindex='-1'])"
      );
      (focusable || dlg).focus();
    });
    return () => {
      document.body.classList.remove("admin-modal-open");
      previouslyFocused.current?.focus?.();
    };
  }, [open]);

  // Escape to close + focus trap
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close();
        return;
      }
      if (e.key === "Tab") {
        const dlg = dialogRef.current;
        if (!dlg) return;
        const focusables = Array.from(
          dlg.querySelectorAll<HTMLElement>(
            "input, select, textarea, button, [href], [tabindex]:not([tabindex='-1'])"
          )
        ).filter((el) => !el.hasAttribute("disabled"));
        if (focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, close]);

  if (!open) return null;

  const sizeCls = size === "sm" ? "admin-modal-sm" : size === "lg" ? "admin-modal-lg" : "";

  return (
    <div
      ref={overlayRef}
      className="admin-modal-overlay"
      onClick={(e) => {
        if (e.target === overlayRef.current) close();
      }}
      role="presentation"
    >
      <div
        ref={dialogRef}
        className={`admin-modal ${sizeCls}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="admin-modal-title"
        tabIndex={-1}
      >
        <div className="admin-modal-header">
          <h3 id="admin-modal-title">{title}</h3>
          {!persistent && (
            <button
              className="admin-modal-close"
              onClick={close}
              aria-label="关闭"
              type="button"
            >
              ×
            </button>
          )}
        </div>
        <div className="admin-modal-body">{children}</div>
        {footer && <div className="admin-modal-footer">{footer}</div>}
      </div>
    </div>
  );
}
