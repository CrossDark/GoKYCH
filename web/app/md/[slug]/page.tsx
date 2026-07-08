import { getArticle, getAllArticleSlugs } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

// Prerender every existing md article at build (○ Static) — see
// wikidot/[slug] for the full rationale. revalidate=86400 (1d); the
// on-demand webhook handles edits so a long window is safe.
export const revalidate = 86400;
export const dynamicParams = true;
export async function generateStaticParams() {
  return getAllArticleSlugs("md");
}

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const siteTitle = await getSiteTitle();
  try {
    // React.cache in api.ts deduplicates this with the page component's
    // getArticle() call — only one real network request is made.
    const d = await getArticle("md", slug);
    return { title: formatPageTitle(d.article.title, siteTitle) };
  } catch (err) {
    console.error(
      `[md/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: formatPageTitle("文章", siteTitle) };
  }
}

export default async function DetailPage({ params }: Props) {
  const { slug } = await params;
  try {
    const data = await getArticle("md", slug);
    // eslint-disable-next-line react-hooks/error-boundaries
    return <ArticleView data={data} articleType="md" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "md", slug });
  }
}
