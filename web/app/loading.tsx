/**
 * Global loading fallback for the public (non-admin) segment.
 *
 * Next.js automatically shows this while a page segment is streaming
 * in. With ISR + React.cache most navigations are <100ms, but the first
 * request after a deploy (or the first visitor to a rarely-accessed
 * page) still has to SSR — this skeleton prevents the white-screen
 * "stall" that makes the site feel unresponsive.
 *
 * We intentionally keep this minimal (no JS/state) so the browser can
 * render it from the initial HTML chunk without waiting for hydration.
 */
export default function Loading() {
  return (
    <div className="page-loading" aria-label="加载中" role="status">
      <div className="loading-skeleton">
        <div className="skeleton skeleton-title" />
        <div className="skeleton skeleton-line skeleton-line-1" />
        <div className="skeleton skeleton-line skeleton-line-2" />
        <div className="skeleton skeleton-line skeleton-line-3" />
        <div className="skeleton skeleton-line skeleton-line-short" />
      </div>
    </div>
  );
}
