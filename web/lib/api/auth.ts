import { request, cache, dedupClient, isSSR } from "./client";
import type { User, CaptchaResponse, LoginResponse } from "@/lib/types";

const _getMeSSR = cache(() =>
  request<{ user: User | null }>("/auth/me", {
    next: { revalidate: 0 },
  })
);

export function getMe() {
  if (isSSR) return _getMeSSR();
  return dedupClient("me", () =>
    request<{ user: User | null }>("/auth/me")
  );
}

const _getCsrfSSR = cache(() =>
  request<CaptchaResponse>("/auth/csrf", {
    next: { revalidate: 0 },
  })
);

export function getCsrf() {
  if (isSSR) return _getCsrfSSR();
  return dedupClient("csrf", () =>
    request<CaptchaResponse>("/auth/csrf")
  );
}

export function login(body: {
  username: string;
  password: string;
  captcha: string;
  csrf_token: string;
}) {
  return request<LoginResponse>("/auth/login", {
    method: "POST",
    headers: { "X-CSRF-Token": body.csrf_token },
    body: JSON.stringify(body),
  });
}

export function logout(csrfToken: string) {
  return request<{ status: string }>("/auth/logout", {
    method: "POST",
    headers: { "X-CSRF-Token": csrfToken },
  });
}