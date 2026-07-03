import { request, cache, isSSR } from "./client";
import type { HomeData } from "@/lib/types";

const _getHomeSSR = cache(() => request<HomeData>("/home", { anon: true, next: { tags: ["home"], revalidate: 3600 } }));

export function getHome() {
  if (isSSR) return _getHomeSSR();
  return request<HomeData>("/home");
}