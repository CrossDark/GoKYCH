import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import type { Metadata } from "next";

// ISR: revalidate article content every 300s (5min). Comments/ratings are fetched
// client-side after hydration, so cached HTML stays fresh enough.
export const revalidate = 300;

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  try {
    // React.cache in api.ts deduplicates this with the page component's
    // getArticle() call — only one real network request is made.
    const d = await getArticle("md", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch (err) {
    console.error(
      `[md/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: "文章 — 跨越晨昏" };
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
