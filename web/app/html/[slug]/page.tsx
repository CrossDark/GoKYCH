import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import type { Metadata } from "next";

export const revalidate = 300;

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  try {
    const d = await getArticle("html", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch (err) {
    console.error(
      `[html/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: "文章 — 跨越晨昏" };
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
