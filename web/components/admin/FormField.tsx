"use client";

import { ReactNode, useId } from "react";

interface FormFieldProps {
  label: string;
  /** Mark as required (renders * next to label, sets `required` on input) */
  required?: boolean;
  /** Optional hint text shown below the input. */
  hint?: string;
  /** Error message — when present, the field is marked invalid. */
  error?: string;
  /** Children that should be the actual input/select/textarea. */
  children: (ids: { id: string; describedBy?: string; invalid: boolean }) => ReactNode;
}

/**
 * Wraps a form input with a label, optional hint, and error text. Handles
 * proper a11y wiring:
 *   - `<label htmlFor={id}>` ↔ child input `id={id}`
 *   - hint / error get `aria-describedby` so screen readers announce them
 *   - error state propagates `aria-invalid` to the child
 *
 * Usage:
 *   <FormField label="标题" required hint="..." error={err}>
 *     {({ id, describedBy, invalid }) => (
 *       <input id={id} aria-describedby={describedBy} aria-invalid={invalid || undefined} ... />
 *     )}
 *   </FormField>
 */
export function FormField({ label, required, hint, error, children }: FormFieldProps) {
  const baseId = useId();
  const id = `f-${baseId}`;
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-err` : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  const invalid = !!error;

  return (
    <div className={`admin-form-group ${invalid ? "has-error" : ""}`}>
      <label htmlFor={id}>
        {label}
        {required && (
          <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
        )}
      </label>
      {children({ id, describedBy, invalid })}
      {hint && !error && (
        <div id={hintId} className="admin-form-hint">
          {hint}
        </div>
      )}
      {error && (
        <div id={errorId} className="admin-form-error" role="alert">
          {error}
        </div>
      )}
    </div>
  );
}
