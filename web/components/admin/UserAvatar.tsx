"use client";

import { useState, useEffect } from "react";
import type { User } from "@/lib/types";

interface UserAvatarProps {
  user: Pick<User, "nickname" | "username" | "avatar"> | null | undefined;
  size?: number;
  className?: string;
  // When true, render the avatar at 1.4× the declared size and clip the
  // letter inside a 1.4× font-size to match the gradient circle's optical
  // centering. Off by default to match the admin list / sidebar specs.
  large?: boolean;
}

/**
 * Admin-area user avatar. Renders the user's chosen avatar URL as an
 * <img> clipped to a circle when present; falls back to the first
 * letter of nickname (or username) on a gradient pill — the original
 * behaviour everywhere in the admin used just the fallback, which is
 * why setting a profile avatar silently did nothing visible.
 *
 * External URLs are passed through unchanged. Site-relative URLs
 * (`/uploads/...`) work too because the Next.js rewrites + nginx serve
 * them from /uploads on api.kych.net, which the browser fetches
 * directly. There is intentionally no crossOrigin attribute: a
 * 3rd-party CDN serving the avatar without ACAO is still shown — a
 * broken CORS response from <img> just fails the decode and we fall
 * back to the letter on next render.
 *
 * The img's onError flips a local flag back to the letter branch so a
 * stale avatar URL (Gravatar returning 404, user deleted the file at
 * /uploads/foo.png) doesn't leave a broken-image icon on screen.
 */
export function UserAvatar({ user, size = 28, className = "", large = false }: UserAvatarProps) {
  const nickname = user?.nickname || "";
  const username = user?.username || "";
  const avatar = user?.avatar?.trim() || "";
  const char = (nickname[0] || username[0] || "?").toUpperCase();
  const fontSize = large ? size * 0.4 : Math.max(size * 0.42, 10);
  // Once we've seen an <img> error for THIS avatar URL we won't try again
  // for this render — re-keying the component (e.g. by changing user.id)
  // resets the flag because the state lives on this instance.
  const [imgFailed, setImgFailed] = useState(false);
  // Reset on URL change so an admin who updates their avatar (e.g. setting
  // a new URL in /admin/profile) sees the new image rather than a stale
  // failure flag from a previous attempt.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setImgFailed(false);
  }, [avatar]);
  const showImg = avatar && !imgFailed;
  return (
    <span
      className={`admin-user-avatar ${className}`}
      style={{
        width: size,
        height: size,
        fontSize,
        position: "relative",
        overflow: "hidden",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        // The base .admin-user-avatar gradient is a fallback for the letter
        // state. When we render an <img> we want the gradient gone — a
        // user's avatar is often a transparent PNG and the blue circle
        // behind it shows through as an unintended backdrop.
        ...(showImg ? { background: "transparent" } : {}),
      }}
      aria-hidden={showImg ? true : undefined}
    >
      {showImg ? (
        <img
          src={avatar}
          alt=""
          referrerPolicy="no-referrer"
          onError={() => setImgFailed(true)}
          style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }}
        />
      ) : (
        char
      )}
    </span>
  );
}