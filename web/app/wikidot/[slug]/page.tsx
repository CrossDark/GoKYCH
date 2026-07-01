import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import type { Metadata } from "next";

export const revalidate = 60;

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  try {
    const d = await getArticle("wikidot", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch (err) {
    console.error(
      `[wikidot/${slug}] generateMetadata fetch failed`,
      err instanceof Error ? err.message : err
    );
    return { title: "文章 — 跨越晨昏" };
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
