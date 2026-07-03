import { listLabels } from "@/lib/api";
import { getSiteTitle, formatPageTitle } from "@/lib/site-title";
import Link from "next/link";
import type { Metadata } from "next";

export const revalidate = 3600; // tag cloud changes less frequently

export async function generateMetadata(): Promise<Metadata> {
  const siteTitle = await getSiteTitle();
  return { title: formatPageTitle("标签", siteTitle) };
}

export default async function LabelsPage() {
  const tags = await listLabels().catch(() => []);

  return (
    <div className="page labels-page">
      <h1>标签</h1>
      {tags.length === 0 ? (
        <p className="empty-message">暂无标签。</p>
      ) : (
        <div className="tag-cloud">
          {tags.map((tag) => (
            <Link
              key={tag.id}
              href={`/labels/${tag.name}`}
              className="tag-cloud-item"
            >
              <span className="tag-name">{tag.name}</span>
              <span className="tag-count">{tag.count}</span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
