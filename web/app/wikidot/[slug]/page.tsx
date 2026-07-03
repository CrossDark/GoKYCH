import { getArticle, getAllArticleSlugs } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

export const revalidate = 86400;
// Prerender every existing wikidot article at build (○ Static) so EdgeOne
// serves them as static assets — no SSR/SCF on first hit. generateStaticParams
// fetches all slugs of this type from the API at build; articles created
// after a build are still covered by on-demand ISR (dynamicParams=true)
// plus the revalidate webhook. revalidate=86400 (1d): the webhook does
// on-demand invalidation on edits, so a long window is safe and maximises
// cache hits.
export const dynamicParams = true;
export async function generateStaticParams() {
  return getAllArticleSlugs("wikidot");
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
