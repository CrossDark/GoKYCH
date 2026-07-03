import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

export const revalidate = 1800;

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const siteTitle = await getSiteTitle();
  try {
    const d = await getArticle("html", slug);
    return { title: formatPageTitle(d.article.title, siteTitle) };
  } catch (err) {
    console.error(
      `[html/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: formatPageTitle("文章", siteTitle) };
  }
}

export default async function DetailPage({ params }: Props) {
  const { slug } = await params;
  try {
    const data = await getArticle("html", slug);
    return <ArticleView data={data} articleType="html" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "html", slug });
  }
}
