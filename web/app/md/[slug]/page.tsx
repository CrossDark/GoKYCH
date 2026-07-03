import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

// ISR: revalidate article content every 1800s (30min). Comments/ratings are
// fetched client-side after hydration, so cached HTML stays fresh enough.
// generateStaticParams([]) + dynamicParams=true opts the [slug] route into
// on-demand ISR (●) so EdgeOne/CF Workers can cache the HTML; without it
// the route is ƒ Dynamic and emits `private, no-store` (uncacheable).
export const revalidate = 1800;
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
    return <ArticleView data={data} articleType="md" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "md", slug });
  }
}
