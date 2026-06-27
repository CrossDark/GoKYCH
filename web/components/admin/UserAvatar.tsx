"use client";

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
 */
export function UserAvatar({ user, size = 28, className = "", large = false }: UserAvatarProps) {
  const nickname = user?.nickname || "";
  const username = user?.username || "";
  const avatar = user?.avatar?.trim() || "";
  const char = (nickname[0] || username[0] || "?").toUpperCase();
  const fontSize = large ? size * 0.4 : Math.max(size * 0.42, 10);
  // Outer wrapper keeps the gradient as the visible base if the <img>
  // fails to load (network error, CORS, 404 on /uploads/...); the img
  // sits on top and is fully circular via overflow:hidden.
  return (
    <span
      className={`admin-user-avatar ${className}`}
      style={{ width: size, height: size, fontSize, position: "relative", overflow: "hidden", display: "inline-flex", alignItems: "center", justifyContent: "center" }}
      aria-hidden={avatar ? true : undefined}
    >
      {avatar ? (
        <img
          src={avatar}
          alt=""
          referrerPolicy="no-referrer"
          style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }}
        />
      ) : (
        char
      )}
    </span>
  );
}