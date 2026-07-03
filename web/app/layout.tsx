import type { Metadata } from "next";
import { headers } from "next/headers";
import { ThemeProvider } from "@/components/ThemeProvider";
import { ThemeStylesheet } from "@/components/ThemeStylesheet";
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
  const headersList = await headers();
  const nonce = headersList.get("X-Nonce") || undefined;

  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script
          nonce={nonce}
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
        <ThemeStylesheet />
        <ThemeProvider>
          <LayoutWrapper>{children}</LayoutWrapper>
        </ThemeProvider>
      </body>
    </html>
  );
}
