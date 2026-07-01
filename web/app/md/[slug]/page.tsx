import { getArticle } from "@/lib/api";
import { renderArticleDetailError } from "@/components/ArticleDetailError";
import { ArticleView } from "@/components/ArticleView";
import { cookies } from "next/headers";
import type { Metadata } from "next";

export const dynamic = "force-dynamic";

interface Props {
  params: Promise<{ slug: string }>;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  try {
    const d = await getArticle("md", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch {
    return { title: "文章 — 跨越晨昏" };
  }
}

export default async function DetailPage({ params }: Props) {
  const { slug } = await params;
  try {
    const data = await getArticle("md", slug);
    // Extract CSRF token from cookies for the client components.
    return <ArticleView data={data} articleType="md" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "md", slug });
  }
}
