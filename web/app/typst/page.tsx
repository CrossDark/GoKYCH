import ArticleList from "@/components/ArticleList";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

export const revalidate = 300;

export async function generateMetadata(): Promise<Metadata> {
  const siteTitle = await getSiteTitle();
  return { title: formatPageTitle("Typst 文章", siteTitle) };
}

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ page?: string }>;
}) {
  const { page: pageStr } = await searchParams;
  const page = parseInt(pageStr || "1", 10) || 1;
  return <ArticleList type="typst" page={page} title="Typst 文章" />;
}
