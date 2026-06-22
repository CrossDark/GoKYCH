import Link from "next/link";

interface Props {
  page: number;
  totalPages: number;
  basePath: string; // e.g. "/md" or "/search?q=xxx&"
}

export function Pagination({ page, totalPages, basePath }: Props) {
  if (totalPages <= 1) return null;

  const separator = basePath.includes("?") ? "&" : "?";
  const pages = [];
  for (let i = 1; i <= totalPages; i++) {
    if (i === 1 || i === totalPages || (i >= page - 2 && i <= page + 2)) {
      pages.push(i);
    } else if (pages[pages.length - 1] !== -1) {
      pages.push(-1); // ellipsis placeholder
    }
  }

  return (
    <nav className="pagination" aria-label="分页导航">
      {page > 1 && (
        <Link href={`${basePath}${separator}page=${page - 1}`} className="page-link">
          ‹ 上一页
        </Link>
      )}
      {pages.map((p, i) =>
        p === -1 ? (
          <span key={`e${i}`} className="page-ellipsis">…</span>
        ) : (
          <Link
            key={p}
            href={`${basePath}${separator}page=${p}`}
            className={`page-link ${p === page ? "active" : ""}`}
          >
            {p}
          </Link>
        )
      )}
      {page < totalPages && (
        <Link href={`${basePath}${separator}page=${page + 1}`} className="page-link">
          下一页 ›
        </Link>
      )}
    </nav>
  );
}
