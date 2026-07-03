import { NextResponse } from "next/server";
import { revalidateTag, revalidatePath } from "next/cache";

// On-demand ISR revalidation endpoint. The Go backend POSTs here
// (fire-and-forget, see internal/api/revalidate.go) right after a
// successful article/comment/rating/label/settings mutation so the long
// ISR windows (article detail = 1800s, home = 300s, site = 600s) don't
// delay showing edits to other viewers.
//
// Lives at /revalidate (NOT /api/revalidate) because next.config.ts has a
// dev rewrite proxying /api/* to the Go backend on :8000; /revalidate is
// outside that prefix so the route handler is reached directly in both
// dev and prod.
//
// Auth: a shared bearer token (REVALIDATE_SECRET) — the same secret is
// set on the backend (.env REVALIDATE_SECRET) and on each frontend
// (Cloudflare Workers secret / EdgeOne env var). Without it anyone could
// purge caches.

export async function POST(req: Request) {
  const secret = process.env.REVALIDATE_SECRET;
  if (!secret) {
    return NextResponse.json(
      { error: "REVALIDATE_SECRET not configured on frontend" },
      { status: 500 }
    );
  }
  const auth = req.headers.get("authorization") || "";
  if (auth !== `Bearer ${secret}`) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }

  const body = await req.json().catch(() => ({})) as {
    tags?: string[];
    paths?: string[];
  };
  const tags = body.tags ?? [];
  const paths = body.paths ?? [];

  let ok = true;
  for (const t of tags) {
    try {
      // Next 16 made the cache-life profile arg required; "max" is the
      // documented replacement for the old single-arg on-demand purge.
      revalidateTag(t, "max");
    } catch {
      ok = false;
    }
  }
  for (const p of paths) {
    try {
      revalidatePath(p, "page");
    } catch {
      ok = false;
    }
  }

  return NextResponse.json({ revalidated: ok, tags, paths });
}

// HEAD/GET convenience probe so operators can curl the endpoint without a
// body to confirm it is reachable (auth still required for the mutating
// POST). GET returns 200 once the route is deployed.
export async function GET() {
  return NextResponse.json({ ok: true, endpoint: "revalidate" });
}
