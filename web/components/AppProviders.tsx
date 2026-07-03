"use client";

import {
  createContext,
  useContext,
  useState,
  useEffect,
  type ReactNode,
} from "react";
import { getMe, getCsrf } from "@/lib/api";
import type { User, SiteConfig, TagWithCount, Theme } from "@/lib/types";

// AppProviders centralises the per-page auth + public-site data that used
// to be re-fetched independently by Header / ThemeStylesheet / ArticleView /
// RatingWidget / CommentSection. On a public article page this cuts the
// post-hydration cross-origin request waterfall from ~5–8 down to a single
// getMe (and a getCsrf only when the user is logged in).
//
// `site` / `labels` / `themes` are SSR-fetched by <AppData> (anon + cached,
// no cookies() touched, so ISR Full Route Cache stays intact once the
// per-request nonce is removed) and passed in as the initial values. The
// auth pair (getMe/getCsrf) runs once on the client so it never lands on
// the SSR critical path or defeats HTML caching.
interface AppContextValue {
  site: SiteConfig | null;
  labels: TagWithCount[];
  themes: Theme[];
  user: User | null;
  csrfToken: string;
  authChecked: boolean;
}

const AppContext = createContext<AppContextValue>({
  site: null,
  labels: [],
  themes: [],
  user: null,
  csrfToken: "",
  authChecked: false,
});

export function useApp(): AppContextValue {
  return useContext(AppContext);
}

export function AppProviders({
  site,
  labels,
  themes,
  children,
}: {
  site: SiteConfig | null;
  labels: TagWithCount[];
  themes: Theme[];
  children: ReactNode;
}) {
  const [user, setUser] = useState<User | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [authChecked, setAuthChecked] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getMe()
      .then((r) => {
        if (cancelled) return;
        if (r.user) {
          setUser(r.user);
          getCsrf()
            .then((c) => {
              if (!cancelled) setCsrfToken(c.csrf_token);
            })
            .catch(() => {});
        }
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setAuthChecked(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <AppContext.Provider
      value={{ site, labels, themes, user, csrfToken, authChecked }}
    >
      {children}
    </AppContext.Provider>
  );
}
