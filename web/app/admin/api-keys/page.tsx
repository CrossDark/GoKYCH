"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { getCsrf, getMe, listApiKeys, createApiKey, deleteApiKey } from "@/lib/api";
import type { ApiKey, CreateApiKeyResponse } from "@/lib/types";
import { useToast } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";
import { fmtDateTimeShort } from "@/lib/format";

export default function AdminAPIKeys() {
  const router = useRouter();
  const [csrf, setCsrf] = useState("");
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [justCreated, setJustCreated] = useState<CreateApiKeyResponse | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [pendingDelete, setPendingDelete] = useState<ApiKey | null>(null);
  const toast = useToast();

  const loadKeys = async (token: string) => {
    try {
      const d = await listApiKeys(token);
      setKeys(d);
    } catch {
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    getMe().then((r) => {
      if (!r.user || r.user.role !== "owner") {
        router.replace("/admin");
        return;
      }
      getCsrf().then((c) => {
        setCsrf(c.csrf_token);
        loadKeys(c.csrf_token);
      });
    }).catch(() => setLoading(false));
  }, []);

  const createKey = async () => {
    if (!newName.trim()) {
      toast.warning("请填写名称。");
      return;
    }
    setCreating(true);
    try {
      const data = await createApiKey(csrf, newName.trim());
      setJustCreated(data);
      setNewName("");
      loadKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "创建失败。");
    } finally {
      setCreating(false);
    }
  };

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    setDeletingId(pendingDelete.id);
    try {
      await deleteApiKey(csrf, pendingDelete.id);
      toast.success(`已撤销「${pendingDelete.name}」。`);
      loadKeys(csrf);
    } catch (err: any) {
      toast.error(err.message || "撤销失败。");
    } finally {
      setDeletingId(null);
      setPendingDelete(null);
    }
  };

  if (loading) return <div className="admin-page"><p>加载中…</p></div>;

  return (
    <div className="admin-page admin-api-keys">
      <div className="admin-page-header">
        <div>
          <h1>API Key</h1>
          <div className="admin-page-subtitle">
            用于脚本/插件/CI 调用后端，无需登录或验证码。请求时在 <code>X-API-Key</code> 头里带上。
          </div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>➕ 新建</h2>
        </div>
        <div className="admin-card-body">
          <div style={{ display: "flex", gap: 8 }}>
            <input
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="例如 CI 部署 / 数据导出脚本"
              style={{ flex: 1 }}
              onKeyDown={(e) => { if (e.key === "Enter") createKey(); }}
            />
            <button
              className="admin-btn admin-btn-primary"
              onClick={createKey}
              disabled={creating}
            >
              {creating ? "创建中…" : "创建 Key"}
            </button>
          </div>
          <p style={{ marginTop: 8, color: "var(--text-muted)", fontSize: "0.82rem" }}>
            创建后明文只展示一次。立即复制保存，刷新页面后无法再次查看。
          </p>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header">
          <h2>📋 已创建的 Key</h2>
        </div>
        <div className="admin-card-body no-padding">
          {keys.length === 0 ? (
            <div className="admin-empty">还没有 API Key</div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>前缀</th>
                  <th className="col-date">创建时间</th>
                  <th className="col-date">最后使用</th>
                  <th className="col-date">过期</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id}>
                    <td>{k.name}</td>
                    <td><code>{k.key_prefix}…</code></td>
                    <td className="col-date">{fmtDateTimeShort(k.created_at)}</td>
                    <td className="col-date">{fmtDateTimeShort(k.last_used_at)}</td>
                    <td className="col-date">{fmtDateTimeShort(k.expires_at)}</td>
                    <td className="col-actions">
                      <div className="admin-table-actions">
                        <button
                          className="admin-btn admin-btn-danger admin-btn-sm"
                          onClick={() => setPendingDelete(k)}
                          disabled={deletingId === k.id}
                        >
                          🗑 撤销
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <AdminModal
        open={!!justCreated}
        onClose={() => setJustCreated(null)}
        title="🔑 新的 API Key"
        size="md"
      >
        {justCreated && (
          <div>
            <p style={{ marginBottom: 12, color: "var(--text-muted)" }}>
              <strong style={{ color: "#e74c3c" }}>请立即复制下面的完整 Key：</strong>
              <br />
              此明文仅展示一次。关闭后无法再次查看，如丢失需重新创建。
            </p>
            <pre
              style={{
                background: "var(--code-bg)",
                border: "1px solid var(--border)",
                borderRadius: 4,
                padding: 12,
                fontFamily: "monospace",
                fontSize: "0.85rem",
                overflowX: "auto",
                userSelect: "all",
              }}
            >
              {justCreated.plaintext_key}
            </pre>
            <div style={{ display: "flex", gap: 8, marginTop: 12 }}>
              <button
                className="admin-btn admin-btn-secondary"
                onClick={() => {
                  if (justCreated.plaintext_key) {
                    navigator.clipboard.writeText(justCreated.plaintext_key);
                    toast.success("已复制到剪贴板。");
                  }
                }}
              >
                📋 复制
              </button>
              <button
                className="admin-btn admin-btn-primary"
                onClick={() => setJustCreated(null)}
              >
                我已保存
              </button>
            </div>
          </div>
        )}
      </AdminModal>

      <AdminModal
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        title="撤销 API Key"
        size="sm"
      >
        {pendingDelete && (
          <div>
            <p>确定要撤销「<strong>{pendingDelete.name}</strong>」？</p>
            <p style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>
              前缀 <code>{pendingDelete.key_prefix}…</code>。
              撤销后使用此 Key 的脚本/插件将立即无法调用 API。此操作不可撤销。
            </p>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button
                className="admin-btn admin-btn-ghost"
                onClick={() => setPendingDelete(null)}
              >
                取消
              </button>
              <button
                className="admin-btn admin-btn-danger"
                onClick={confirmDelete}
                disabled={!!deletingId}
              >
                🗑 撤销
              </button>
            </div>
          </div>
        )}
      </AdminModal>
    </div>
  );
}