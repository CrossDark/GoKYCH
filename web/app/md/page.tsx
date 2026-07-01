import ArticleList from "@/components/ArticleList";
import type { Metadata } from "next";

// ISR: revalidate article lists every 60s so new articles appear quickly.
export const revalidate = 60;

export const metadata: Metadata = { title: "Markdown 文章 — 跨越晨昏" };

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ page?: string }>;
}) {
  const { page: pageStr } = await searchParams;
  const page = parseInt(pageStr || "1", 10) || 1;
  return <ArticleList type="md" page={page} title="Markdown 文章" />;
}
