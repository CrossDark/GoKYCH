// Date formatting helpers for the admin UI and article views.
//
// Previously each page called `new Date(...).toLocaleString("zh-CN", …)`
// inline with slightly different options (dateStyle/timeStyle/month-numeric
// etc.), producing inconsistent output. Centralising here keeps the locale
// and option set in one greppable place.

type DateInput = string | number | Date | null | undefined;

function toDate(s: DateInput): Date | null {
  if (s == null) return null;
  const d = new Date(s);
  if (isNaN(d.getTime())) return null;
  return d;
}

/** Full date + time, e.g. "2026/7/2 14:30:05". Use for admin tables. */
export function fmtDateTime(s: DateInput): string {
  const d = toDate(s);
  return d ? d.toLocaleString("zh-CN") : "—";
}

/** Date only, e.g. "2026/7/2". Use for "注册于" / "发布于" labels. */
export function fmtDate(s: DateInput): string {
  const d = toDate(s);
  return d ? d.toLocaleDateString("zh-CN") : "—";
}

/** Short date + time, e.g. "26/7/2 14:30". Use for compact admin cells. */
export function fmtDateTimeShort(s: DateInput): string {
  const d = toDate(s);
  return d ? d.toLocaleString("zh-CN", { dateStyle: "short", timeStyle: "short" }) : "—";
}

/** Month + day + time, e.g. "7/2 14:30". Use for notification list rows. */
export function fmtMonthDayTime(s: DateInput): string {
  const d = toDate(s);
  if (!d) return "—";
  return d.toLocaleString("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Long date with weekday, e.g. "7月2日星期三". Use for dashboard "today". */
export function fmtLongDate(s: DateInput): string {
  const d = toDate(s);
  if (!d) return "—";
  return d.toLocaleString("zh-CN", {
    month: "long",
    day: "numeric",
    weekday: "long",
  });
}

/**
 * Relative time, e.g. "刚刚" / "3 分钟前" / "2 小时前" / "5 天前".
 * Falls back to fmtDate for anything older than a week.
 */
export function fmtRelative(s: DateInput): string {
  const d = toDate(s);
  if (!d) return "—";
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return "刚刚";
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
  return fmtDate(d);
}
