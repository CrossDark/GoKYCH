import { getArticle } from "@/lib/api";
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
    const d = await getArticle("bbcode", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch {
    return { title: "文章 — 跨越晨昏" };
  }
}

export default async function DetailPage({ params }: Props) {
  const { slug } = await params;
  try {
    const data = await getArticle("bbcode", slug);
    // Extract CSRF token from cookies for the client components.
    return <ArticleView data={data} articleType="bbcode" articleSlug={slug} />;
  } catch {
    return (
      <div className="page">
        <h1>文章不存在</h1>
        <p>该文章可能已被删除，或地址不正确。</p>
      </div>
    );
  }
}
