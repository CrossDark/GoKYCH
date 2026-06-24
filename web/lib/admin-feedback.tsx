"use client";

import { createContext, useContext, useState, useCallback, useEffect, ReactNode } from "react";

export type ToastKind = "success" | "error" | "warning" | "info";

interface Toast {
  id: number;
  kind: ToastKind;
  text: string;
  /** Auto-dismiss after this many ms; 0 = sticky. */
  ttl: number;
}

interface ToastApi {
  show: (text: string, kind?: ToastKind, ttl?: number) => void;
  success: (text: string, ttl?: number) => void;
  error: (text: string, ttl?: number) => void;
  warning: (text: string, ttl?: number) => void;
  info: (text: string, ttl?: number) => void;
  dismiss: (id: number) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const DEFAULT_TTL: Record<ToastKind, number> = {
  success: 3500,
  info: 4000,
  warning: 5000,
  error: 6000,
};

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const show = useCallback(
    (text: string, kind: ToastKind = "info", ttl?: number) => {
      const id = nextId++;
      const t: Toast = { id, kind, text, ttl: ttl ?? DEFAULT_TTL[kind] };
      setToasts((prev) => [...prev, t]);
      if (t.ttl > 0) {
        setTimeout(() => dismiss(id), t.ttl);
      }
    },
    [dismiss]
  );

  const api: ToastApi = {
    show,
    success: (text, ttl) => show(text, "success", ttl),
    error: (text, ttl) => show(text, "error", ttl),
    warning: (text, ttl) => show(text, "warning", ttl),
    info: (text, ttl) => show(text, "info", ttl),
    dismiss,
  };

  return (
    <ToastContext.Provider value={api}>
      {children}
      <ToastContainer toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    // Fallback: console-only when no provider is mounted (e.g. SSR / story)
    return {
      show: (text, kind) => {
        if (typeof console !== "undefined") console[kind === "error" ? "error" : "log"](`[toast:${kind}]`, text);
      },
      success: (text) => { if (typeof console !== "undefined") console.log("[toast:success]", text); },
      error: (text) => { if (typeof console !== "undefined") console.error("[toast:error]", text); },
      warning: (text) => { if (typeof console !== "undefined") console.warn("[toast:warning]", text); },
      info: (text) => { if (typeof console !== "undefined") console.log("[toast:info]", text); },
      dismiss: () => {},
    };
  }
  return ctx;
}

function ToastContainer({
  toasts,
  onDismiss,
}: {
  toasts: Toast[];
  onDismiss: (id: number) => void;
}) {
  if (toasts.length === 0) return null;
  return (
    <div className="admin-toast-container" role="region" aria-live="polite" aria-label="通知">
      {toasts.map((t) => (
        <div key={t.id} className={`admin-toast admin-toast-${t.kind}`}>
          <span>{t.text}</span>
          <button
            className="admin-toast-dismiss"
            onClick={() => onDismiss(t.id)}
            aria-label="关闭通知"
            type="button"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}

/**
 * Convenience hook for one-shot inline messages.
 * Returns [message, showMessage]; `showMessage` shows an admin-notice block.
 * If you also want toast support, use `useToast` directly.
 */
export function useNotice(
  initialKind: ToastKind = "info",
  initialText = ""
): [
  { kind: ToastKind; text: string } | null,
  (kind: ToastKind, text: string) => void,
  () => void
] {
  const [state, setState] = useState<{ kind: ToastKind; text: string } | null>(
    initialText ? { kind: initialKind, text: initialText } : null
  );
  const show = useCallback((kind: ToastKind, text: string) => setState({ kind, text }), []);
  const clear = useCallback(() => setState(null), []);
  return [state, show, clear];
}

/** Tiny helper: register a one-time beforeunload warning when `active` is true. */
export function useBeforeUnload(active: boolean, message = "您有未保存的修改，确定离开吗？") {
  useEffect(() => {
    if (!active) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Most modern browsers ignore custom messages; the presence of preventDefault is what triggers the prompt.
      e.returnValue = message;
      return message;
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [active, message]);
}
