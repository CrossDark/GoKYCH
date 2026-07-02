// WebAuthn helpers shared by the login page and the admin profile page.
// Previously these were duplicated byte-for-byte across the two pages
// (the profile page even carried a TODO comment acknowledging the copy);
// extracting them here keeps the encoding logic in one greppable place.

/**
 * Returns true if the browser can run WebAuthn (Touch ID / Windows Hello
 * / Android biometrics / hardware key). On non-supporting browsers we
 * hide the passkey button entirely rather than let the user click and
 * see a "navigator.credentials is undefined" error.
 */
export function supportsWebAuthn(): boolean {
  if (typeof window === "undefined") return false;
  const w = window as any;
  return !!(w.PublicKeyCredential && typeof w.PublicKeyCredential === "function");
}

/** Encode an ArrayBuffer as a base64url string (no padding). */
export function arrayBufferToBase64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Decode a base64url string back into an ArrayBuffer. */
export function base64UrlToArrayBuffer(s: string): ArrayBuffer {
  const padded = s.replace(/-/g, "+").replace(/_/g, "/") + "==".slice(0, (4 - (s.length % 4)) % 4);
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}
