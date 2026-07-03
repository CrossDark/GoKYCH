import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import type { Metadata } from "next";

export async function generateMetadata(): Promise<Metadata> {
  const siteTitle = await getSiteTitle();
  return { title: formatPageTitle("关于", siteTitle) };
}

export default async function AboutPage() {
  const siteTitle = await getSiteTitle();
  return (
    <div className="page about-page">
      <h1>关于</h1>
      <p>
        {siteTitle}是一个个人网站，使用 Go + Gin 构建后端 API，React + Next.js
        构建前端界面。支持 Markdown、Wikidot、HTML、BBCode、Typst 五种内容格式。
      </p>
      <h2>技术栈</h2>
      <ul>
        <li><strong>后端</strong>：Go + Gin + MySQL</li>
        <li><strong>前端</strong>：React + Next.js + TypeScript</li>
        <li><strong>部署</strong>：Docker + Nginx</li>
      </ul>
    </div>
  );
}
