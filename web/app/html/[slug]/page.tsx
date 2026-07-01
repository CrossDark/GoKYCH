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
    const d = await getArticle("html", slug);
    return { title: `${d.article.title} — 跨越晨昏` };
  } catch (err) {
    // generateMetadata only sets <title>; the main page below is the
    // one the reader sees, and it shares the same `getArticle()` call.
    // We log here so a broken backend config (e.g. missing
    // NEXT_PUBLIC_API_BASE_URL on EdgeOne) surfaces in the SSR log
    // even when the title fallback hides it from the reader.
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
    // Extract CSRF token from cookies for the client components.
    return <ArticleView data={data} articleType="html" articleSlug={slug} />;
  } catch (err) {
    return renderArticleDetailError(err, { type: "html", slug });
  }
}
