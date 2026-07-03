import { getSite } from "./api";

const DEFAULT_SITE_TITLE = "跨越晨昏";

export async function getSiteTitle(): Promise<string> {
  try {
    const site = await getSite();
    return site?.site?.title || DEFAULT_SITE_TITLE;
  } catch {
    return DEFAULT_SITE_TITLE;
  }
}

export function formatPageTitle(pageName: string, siteTitle: string): string {
  return `${pageName} — ${siteTitle}`;
}
