import type { Metadata } from "next";
import Script from "next/script";
import { ThemeProvider } from "@/components/ThemeProvider";
import { ThemeStylesheet } from "@/components/ThemeStylesheet";
import { AppData } from "@/components/AppData";
import { LayoutWrapper } from "./LayoutWrapper";
import { getSiteTitle } from "@/lib/site-title";
import "@/styles/globals.css";

export async function generateMetadata(): Promise<Metadata> {
  const title = await getSiteTitle();
  return {
    title,
    description: "个人网站",
  };
}

export default async function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // NOTE: this layout deliberately does NOT call headers()/cookies().
  // The previous per-request CSP nonce (read via `await headers()` here)
  // forced every route into dynamic rendering and disabled Next.js Full
  // Route Cache, so neither EdgeOne nor Cloudflare Workers could cache the
  // SSR HTML at the edge. CSP is now a static policy set in middleware.ts
  // (`script-src 'self' 'unsafe-inline'`), and the inline theme-detection
  // script below runs under that 'unsafe-inline' allowance. Server-side
  // sanitisation (bluemonday / DOMPurify / goldmark) remains the primary
  // XSS defence. Restoring ISR lets both frontends serve cached HTML,
  // which is the main "space-for-time" win for the mainland audience.
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        {/* Theme bootstrap — must run before React hydration so the
         * dark/light class matches the SSR baseline. `next/script`
         * with `strategy="beforeInteractive"` + a stable `id` is the
         * only path that:
         *   1. renders the inline content server-side (so the user's
         *      theme is already correct before the first paint), and
         *   2. skips React's "script inside component tree" warning
         *      that hits `<script dangerouslySetInnerHTML>` directly,
         *   3. dedupes — without the id, Next.js dev re-injects this
         *      script on every HMR refresh and the second copy breaks
         *      React 19's hydration diff.
         */}
        <Script
          id="kokoro-theme-init"
          strategy="beforeInteractive"
          dangerouslySetInnerHTML={{
            __html: `
              (function() {
                try {
                  var theme = localStorage.getItem('theme');
                  if (theme === 'dark' || (!theme && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
                    document.documentElement.setAttribute('data-theme', 'dark');
                  } else {
                    document.documentElement.setAttribute('data-theme', 'light');
                  }
                } catch(e) {}
              })();
            `,
          }}
        />
      </head>
      <body>
        <ThemeProvider>
          <AppData>
            <ThemeStylesheet />
            <LayoutWrapper>{children}</LayoutWrapper>
          </AppData>
        </ThemeProvider>
      </body>
    </html>
  );
}
