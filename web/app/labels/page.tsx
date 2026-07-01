import { listLabels } from "@/lib/api";
import Link from "next/link";
import type { Metadata } from "next";

export const revalidate = 120; // tag cloud changes less frequently

export const metadata: Metadata = { title: "标签 — 跨越晨昏" };

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
