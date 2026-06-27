"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { getCsrf, getMe, apiUrl, apiFetch } from "@/lib/api";
import { useToast } from "@/lib/admin-feedback";
import { AdminModal } from "@/components/admin/AdminModal";

interface AnyUserPasskey {
  id: number;
  user_id: number;
  user_name: string;
  user_nickname: string;
  name: string;
  credential_id: string;
  transports: string[];
  sign_count: number;
  created_at: string;
}

export default function AdminPasskeys() {
  const router = useRouter();
  const [csrf, setCsrf] = useState("");
  const [keys, setKeys] = useState<AnyUserPasskey[]>([]);
  const [loading, setLoading] = useState(true);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [pendingDelete, setPendingDelete] = useState<AnyUserPasskey | null>(null);
  const toast = useToast();

  useEffect(() => {
    // Backend gates these endpoints with requireOwner; mirror that here so
    // a non-owner who hits /admin/passkeys directly gets sent back to the
    // dashboard instead of a wall of 403s.
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

  const loadKeys = (token: string) => {
    apiFetch(apiUrl("/api/admin/passkeys"), { headers: { "X-CSRF-Token": token } })
      .then((r) => r.json())
      .then((d: AnyUserPasskey[]) => { setKeys(d || []); setLoading(false); })
      .catch(() => setLoading(false));
  };

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    setDeletingId(pendingDelete.id);
    try {
      const res = await apiFetch(apiUrl(`/api/admin/passkeys/${pendingDelete.id}`), {
        method: "DELETE",
        headers: { "X-CSRF-Token": csrf },
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.error || `撤销失败 (${res.status})`);
      }
      toast.success(`已撤销「${pendingDelete.name}」（用户 @${pendingDelete.user_name}）。`);
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
    <div className="admin-page admin-passkeys">
      <div className="admin-page-header">
        <div>
          <h1>🛡️ 全站 Passkey 管理</h1>
          <div className="admin-page-subtitle">
            列出所有用户 & 管理员登记的 Passkey（含你自己）。可撤销任意一条。撤销后该设备无法用该 Passkey 登录。
          </div>
        </div>
      </div>

      <div className="admin-card">
        <div className="admin-card-header"><h2>📋 已登记的 Passkey</h2></div>
        <div className="admin-card-body no-padding">
          {keys.length === 0 ? (
            <div className="admin-empty">暂无任何 Passkey</div>
          ) : (
            <table className="admin-table">
              <thead>
                <tr>
                  <th>用户</th>
                  <th>名称</th>
                  <th>凭据 ID</th>
                  <th className="col-date">注册时间</th>
                  <th className="col-actions">操作</th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id}>
                    <td>
                      <div style={{ fontWeight: 600 }}>{k.user_nickname || k.user_name}</div>
                      <div style={{ color: "var(--text-muted)", fontSize: "0.75rem" }}>@{k.user_name}</div>
                    </td>
                    <td>{k.name}</td>
                    <td><code style={{ fontSize: "0.75rem" }}>{k.credential_id.slice(0, 14)}…</code></td>
                    <td className="col-date">{new Date(k.created_at).toLocaleString("zh-CN")}</td>
                    <td className="col-actions">
                      <button
                        className="admin-btn admin-btn-danger admin-btn-sm"
                        onClick={() => setPendingDelete(k)}
                        disabled={deletingId === k.id}
                      >
                        🗑 撤销
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <AdminModal
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        title="撤销用户 Passkey"
        size="sm"
      >
        {pendingDelete && (
          <div>
            <p>确定要撤销「<strong>{pendingDelete.name}</strong>」（属于 <strong>@{pendingDelete.user_name}</strong>）？</p>
            <p style={{ color: "var(--text-muted)", fontSize: "0.85rem" }}>
              站长操作。撤销后该设备立即无法用此 Passkey 登录；被撤销用户的密码登录是否恢复取决于其剩余 Passkey 数量。
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