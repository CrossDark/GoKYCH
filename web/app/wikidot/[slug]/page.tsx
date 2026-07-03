import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

export const revalidate = 1800;
// Opt the dynamic [slug] route into ISR (Full Route Cache). Without
// generateStaticParams, Next.js treats [slug] as ƒ Dynamic and emits
// `Cache-Control: private, no-store` on every response — so neither
// EdgeOne nor Cloudflare Workers could cache article HTML. Returning []
// (with the default dynamicParams=true) makes the route ○ Static/ISR:
// no paths prerendered at build, but on-demand renders are cached for
// `revalidate` seconds. Slugs are user-created so we can't enumerate
// them at build time; on-demand ISR is the right mode.
export const dynamicParams = true;
export async function generateStaticParams() {
  return [];
}

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const siteTitle = await getSiteTitle();
  try {
    const d = await getArticle("wikidot", slug);
    return { title: formatPageTitle(d.article.title, siteTitle) };
  } catch (err) {
    console.error(
      `[wikidot/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: formatPageTitle("文章", siteTitle) };
  }
}

export default async function DetailPage({ params }: Props) {
  const { slug } = await params;
  try {
    const data = await getArticle("wikidot", slug);
    return <ArticleView data={data} articleType="wikidot" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "wikidot", slug });
  }
}
