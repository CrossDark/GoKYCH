import { request, isSSR } from "./client";
import type { RatingSummary, RatingDetail } from "@/lib/types";

export function getRating(type: string, slug: string) {
  return request<RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    isSSR ? { next: { revalidate: 0 } } : undefined
  );
}

export function setRating(
  type: string, slug: string, csrf: string, score: number
) {
  return request<RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "POST",
      headers: { "X-CSRF-Token": csrf },
      body: JSON.stringify({ score }),
    }
  );
}

export function undoRating(type: string, slug: string, csrf: string) {
  return request<RatingSummary>(
    `/articles/${type}/${slug}/rating`,
    {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }
  );
}

export function getRatingDetails(type: string, slug: string) {
  return request<{ ratings: RatingDetail[] }>(
    `/articles/${type}/${slug}/ratings`
  );
}