"use client";

import { useState, useEffect, use } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { getCsrf, getMe, getArticle, updateArticle, deleteArticle } from "@/lib/api";
import type { Article, User, ArticleDetail } from "@/lib/types";
import { useToast, useBeforeUnload } from "@/lib/admin-feedback";
import { AdminConfirm } from "@/components/admin/AdminConfirm";
import { MarkdownEditor } from "@/components/admin/MarkdownEditor";
import { RevisionDrawer, type RevisionAction } from "@/components/admin/RevisionDrawer";
import { RevisionDiffModal } from "@/components/admin/RevisionDiffModal";
import { RevisionRestoreModal } from "@/components/admin/RevisionRestoreModal";
import { fmtDateTime } from "@/lib/format";

const TYPE_LABELS: Record<string, string> = {
  md: "Markdown",
  wikidot: "Wikidot",
  html: "HTML",
  bbcode: "BBCode",
  typst: "Typst",
};

interface PageProps {
  params: Promise<{ type: string; slug: string }>;
}

export default function AdminArticleDetail({ params }: PageProps) {
  const { type, slug } = use(params);
  const router = useRouter();
  const toast = useToast();

  const [csrf, setCsrf] = useState("");
  const [me, setMe] = useState<User | null>(null);
  const [article, setArticle] = useState<Article | null>(null);
  const [canEdit, setCanEdit] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [form, setForm] = useState({ title: "", content: "", tags: "" });
  const [initial, setInitial] = useState({ title: "", content: "", tags: "" });
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  // V5 — revision history drawer. `revisionsOpen` controls visibility;
  // `revisionAction` records the most recent user-initiated action so
  // the diff modal (V5b) / restore modal (V5c) can read it.
  // `currentSeq` is the article's latest revision seq, fed back to us
  // by the drawer's onLoaded callback — used as the `to` in compare
  // mode so "对比 #N" always diffs against the live state.
  const [revisionsOpen, setRevisionsOpen] = useState(false);
  const [revisionAction, setRevisionAction] = useState<RevisionAction | null>(null);
  const [currentSeq, setCurrentSeq] = useState<number | null>(null);

  const isDirty = form.title !== initial.title
    || form.content !== initial.content
    || form.tags !== initial.tags;

  useBeforeUnload(isDirty && !saving && !deleting);

  // Warn before client-side route change (Next.js doesn't fire beforeunload for SPA nav).
  useEffect(() => {
    if (!isDirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [isDirty]);

  useEffect(() => {
    getCsrf().then((r) => setCsrf(r.csrf_token));
    getMe().then((r) => {
      if (r.user) setMe(r.user);
    }).catch(() => {});
    getArticle(type, slug)
      .then((d: ArticleDetail) => {
        const a = d.article;
        setArticle(a);
        setCanEdit(d.can_edit);
        const init = {
          title: a.title,
          content: a.content,
          tags: (a.tags || []).join(", "),
        };
        setForm(init);
        setInitial(init);
      })
      .catch((err) => setLoadError(err.message || "加载失败。"));
  }, [type, slug]);

  // Use the server-returned can_edit flag (which already accounts for
  // allow_all_edit, admin/owner status, and authorship) to decide whether
  // to show the forbidden page.
  useEffect(() => {
    if (!article || me === null) return;
    if (!canEdit) setForbidden(true);
  }, [article, me, canEdit]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!article) return;
    setSaving(true);
    const tags = form.tags.split(",").map((t) => t.trim()).filter(Boolean);
    try {
      const updated = await updateArticle(article.type, article.slug, csrf, {
        title: form.title,
        content: form.content,
        tags,
      });
      setArticle(updated);
      const init = {
        title: updated.title,
        content: updated.content,
        tags: (updated.tags || []).join(", "),
      };
      setForm(init);
      setInitial(init);
      toast.success("文章已保存。");
    } catch (err: any) {
      toast.error(err.message || "保存失败。");
    } finally {
      setSaving(false);
    }
  };

  const handleRevert = () => {
    setForm(initial);
  };

  const doDelete = async () => {
    if (!article) return;
    setDeleting(true);
    try {
      await deleteArticle(article.type, article.slug, csrf);
      toast.success(`文章「${article.title}」已删除。`);
      // Force navigation since beforeunload won't fire
      window.onbeforeunload = null;
      router.push("/admin/articles");
    } catch (err: any) {
      toast.error(err.message || "删除失败。");
      setDeleting(false);
      throw err;
    }
  };

  // V5 — handle a revision action from the drawer. Setting
  // `revisionAction` opens the corresponding modal:
  //   - view / compare  → RevisionDiffModal (V5b)
  //   - restore         → RevisionRestoreModal (V5c)
  // The drawer itself stays the same; only the parent's
  // `revisionAction` state is read by the modals.
  const handleRevisionAction = (action: RevisionAction) => {
    setRevisionAction(action);
  };

  // V5c — handle a successful restore. The server returns the updated
  // article; we re-sync form + initial so the editor immediately
  // reflects the rolled-back content. `isDirty` clears as a side
  // effect (form === initial), and we toast a success message.
  // We also re-fetch the revision list so currentSeq is current —
  // restore creates a new seq row, so a subsequent "对比当前" against
  // it would otherwise diff against the pre-restore state.
  const handleRestored = (restored: Article) => {
    setArticle(restored);
    const init = {
      title: restored.title,
      content: restored.content,
      tags: (restored.tags || []).join(", "),
    };
    setForm(init);
    setInitial(init);
    setRevisionAction(null);
    // Bump currentSeq to reflect the new latest revision. We don't
    // re-fetch the list — we know the new seq is currentSeq+1
    // (server's invariant: restore always creates the next seq),
    // and the drawer's onLoaded callback will re-fetch when the
    // user reopens the drawer.
    const nextSeq = currentSeq === null ? null : currentSeq + 1;
    setCurrentSeq(nextSeq);
    // Toast reflects the seq the user actually rolled back TO, not
    // the new seq we just created. revisionAction was just cleared
    // above so we read it from the previous render's closure. In
    // practice the user just clicked "回滚 to #N", so saying
    // "已回滚到 #N" matches their intent — the new row is #N+1,
    // but that's an audit detail, not what they meant by the action.
    const targetSeq = revisionAction?.kind === "restore" ? revisionAction.seq : null;
    toast.success(
      targetSeq !== null
        ? `已回滚到 #${targetSeq}。`
        : "已回滚。"
    );
  };

  if (loadError) {
    return (
      <div className="admin-article-detail">
        <div className="admin-page-header">
          <div>
            <h1>文章详情</h1>
            <div className="admin-page-subtitle">无法加载文章</div>
          </div>
        </div>
        <div className="admin-card">
          <div className="admin-card-body">
            <div className="admin-notice admin-notice-error">
              <span className="admin-notice-icon">✕</span>
              <div className="admin-notice-content">{loadError}</div>
            </div>
            <div style={{ marginTop: "1rem" }}>
              <Link href="/admin/articles" className="admin-btn admin-btn-ghost">
                ← 返回列表
              </Link>
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (!article) {
    return (
      <div className="admin-article-detail">
        <div className="admin-page-header">
          <div>
            <h1>文章详情</h1>
            <div className="admin-page-subtitle">加载中…</div>
          </div>
        </div>
        <div className="admin-card">
          <div className="admin-card-body">
            <div className="admin-empty">
              <span className="admin-spinner admin-spinner-lg" />
              <div className="admin-empty-title" style={{ marginTop: 8 }}>加载中…</div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  if (forbidden) {
    // Don't render the editor — the user can't actually save anything, and
    // showing the form just invites wasted effort. Bouncing them back to
    // the (filtered) list is the most useful thing we can do.
    return (
      <div className="admin-article-detail">
        <div className="admin-page-header">
          <div>
            <h1>文章详情</h1>
            <div className="admin-page-subtitle">权限不足</div>
          </div>
        </div>
        <div className="admin-card">
          <div className="admin-card-body">
            <div className="admin-notice admin-notice-error">
              <span className="admin-notice-icon">⛔</span>
              <div className="admin-notice-content">
                您没有权限编辑此文章（该文章由其他用户创建）。
              </div>
            </div>
            <div style={{ marginTop: "1rem" }}>
              <Link href="/admin/articles" className="admin-btn admin-btn-ghost">
                ← 返回我的文章
              </Link>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="admin-article-detail">
      <div className="admin-page-header">
        <div style={{ minWidth: 0 }}>
          <h1 style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            ✏️ {article.title}
          </h1>
          <div className="admin-page-subtitle">
            <span className={`admin-badge admin-badge-${
              article.type === "md" ? "primary" :
              article.type === "wikidot" ? "danger" :
              article.type === "html" ? "warning" :
              article.type === "bbcode" ? "success" : "neutral"
            }`}>
              {TYPE_LABELS[article.type] || article.type}
            </span>
            <code style={{ marginLeft: 8, fontSize: "0.85em" }}>/{article.type}/{article.slug}</code>
            {isDirty && (
              <span style={{ marginLeft: 8, color: "var(--admin-warning)", fontSize: "0.85em" }}>
                ● 有未保存的修改
              </span>
            )}
          </div>
        </div>
        <div className="admin-header-actions">
          <Link
            href={`/${article.type}/${article.slug}`}
            target="_blank"
            rel="noopener"
            className="admin-btn admin-btn-outline admin-btn-sm"
          >
            👁 查看
          </Link>
          <Link href="/admin/articles" className="admin-btn admin-btn-ghost admin-btn-sm">
            ← 返回列表
          </Link>
          {/* V5a — opens the right-rail history drawer. The drawer
              itself dispatches 查看 / 对比当前 / 回滚 actions; the
              V5a placeholder just toasts, V5b/V5c will replace with
              modals. */}
          <button
            type="button"
            className="admin-btn admin-btn-outline admin-btn-sm"
            onClick={() => setRevisionsOpen(true)}
            data-testid="open-revisions"
            title="查看/对比/回滚历史版本"
          >
            📜 历史
          </button>
          <button
            type="button"
            className={`admin-btn admin-btn-danger admin-btn-sm ${deleting ? "admin-btn-loading" : ""}`}
            onClick={() => setConfirmDelete(true)}
            disabled={deleting}
          >
            🗑 删除
          </button>
        </div>
      </div>

      <form onSubmit={handleSave} className="admin-card">
        <div className="admin-card-header">
          <h2>编辑内容</h2>
          <div className="admin-card-actions" style={{ fontSize: "0.78rem", color: "var(--text-muted)" }}>
            类型 / Slug 不可修改
          </div>
        </div>
        <div className="admin-card-body">
          <div className="admin-form">
            <div className="admin-form-group">
              <label htmlFor="detail-title">
                标题
                <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
              </label>
              <input
                id="detail-title"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                required
              />
            </div>
            <div className="admin-form-group">
              <label htmlFor="detail-content">
                内容
                <span style={{ color: "var(--admin-danger)", marginLeft: 4 }} aria-hidden="true">*</span>
              </label>
              <MarkdownEditor
                id="detail-content"
                value={form.content}
                onChange={(next) => setForm({ ...form, content: next })}
                type={type}
                rows={20}
                required
                placeholder="支持 Markdown / Wikidot / HTML / BBCode / Typst（取决于类型）"
              />
            </div>
            <div className="admin-form-group">
              <label htmlFor="detail-tags">
                标签 <span style={{ fontWeight: 400, color: "var(--text-muted)" }}>（逗号分隔）</span>
              </label>
              <input
                id="detail-tags"
                value={form.tags}
                onChange={(e) => setForm({ ...form, tags: e.target.value })}
                placeholder="tag1, tag2, tag3"
              />
            </div>
            <div className="admin-form-actions">
              <button
                type="submit"
                className={`admin-btn admin-btn-primary ${saving ? "admin-btn-loading" : ""}`}
                disabled={saving || !isDirty}
              >
                保存修改
              </button>
              <button
                type="button"
                className="admin-btn admin-btn-ghost"
                onClick={handleRevert}
                disabled={!isDirty || saving}
              >
                撤销修改
              </button>
              <div className="admin-form-actions-spacer" />
              {article.updated_at && (
                <span style={{ fontSize: "0.78rem", color: "var(--text-muted)" }}>
                  最后更新：{fmtDateTime(article.updated_at)}
                </span>
              )}
            </div>
          </div>
        </div>
      </form>

      <AdminConfirm
        open={confirmDelete}
        title="删除文章"
        message={`确定要删除「${article.title}」吗？此操作不可撤销。`}
        confirmText="删除"
        variant="danger"
        onConfirm={doDelete}
        onCancel={() => setConfirmDelete(false)}
      />

      {/* V5 — revision history drawer. See RevisionDrawer.tsx for the
          list rendering; RevisionDiffModal (V5b) handles 查看 / 对比
          current via `revisionAction`; RevisionRestoreModal (V5c) will
          handle 回滚 next. */}
      <RevisionDrawer
        open={revisionsOpen}
        onClose={() => setRevisionsOpen(false)}
        type={article.type}
        slug={article.slug}
        onAction={handleRevisionAction}
        onLoaded={setCurrentSeq}
      />

      {/* V5b — view single version + diff against current. Closes
          by setting `revisionAction` back to null. */}
      <RevisionDiffModal
        action={
          revisionAction?.kind === "view" || revisionAction?.kind === "compare"
            ? revisionAction
            : null
        }
        type={article.type}
        slug={article.slug}
        currentSeq={currentSeq}
        onClose={() => setRevisionAction(null)}
      />

      {/* V5c — restore confirmation. Only opens for the restore
          action; the diff modal handles view/compare. On success,
          the page re-syncs form/initial to the returned article
          and bumps currentSeq by 1 (the new revision row). */}
      <RevisionRestoreModal
        seq={
          revisionAction?.kind === "restore" ? revisionAction.seq : null
        }
        type={article.type}
        slug={article.slug}
        csrf={csrf}
        onClose={() => setRevisionAction(null)}
        onRestored={handleRestored}
      />
    </div>
  );
}
