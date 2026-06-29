import type { Metadata } from "next";
import { ThemeProvider } from "@/components/ThemeProvider";
import { ThemeStylesheet } from "@/components/ThemeStylesheet";
import { LayoutWrapper } from "./LayoutWrapper";
import "katex/dist/katex.min.css";
import "@/styles/globals.css";

export const metadata: Metadata = {
  title: "跨越晨昏",
  description: "个人网站",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        {/* Prevent flash of unstyled content for dark mode. */}
        <script
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
