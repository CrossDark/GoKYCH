import ArticleList from "@/components/ArticleList";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

// ISR: revalidate article lists every 60s so new articles appear quickly.
export const revalidate = 3600;

export async function generateMetadata(): Promise<Metadata> {
  const siteTitle = await getSiteTitle();
  return { title: formatPageTitle("Markdown 文章", siteTitle) };
}

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ page?: string }>;
}) {
  const { page: pageStr } = await searchParams;
  const page = parseInt(pageStr || "1", 10) || 1;
  return <ArticleList type="md" page={page} title="Markdown 文章" />;
}
