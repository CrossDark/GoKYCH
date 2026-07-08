import { request } from "./client";
import type { Theme } from "@/lib/types";

export function listThemes() {
  return request<Theme[]>("/themes", { anon: true, next: { revalidate: 30 } });
}