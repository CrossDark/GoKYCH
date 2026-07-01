import ArticleList from "@/components/ArticleList";
import type { Metadata } from "next";

export const revalidate = 60;

export const metadata: Metadata = { title: "Typst 文章 — 跨越晨昏" };

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ page?: string }>;
}) {
  const { page: pageStr } = await searchParams;
  const page = parseInt(pageStr || "1", 10) || 1;
  return <ArticleList type="typst" page={page} title="Typst 文章" />;
}
