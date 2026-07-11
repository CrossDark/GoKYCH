"use client";

import { useState, useEffect, useRef } from "react";
import { getCsrf } from "@/lib/api";
import {
  listDatabaseTables,
  getTableData,
  getTableExportUrl,
  importTableData,
  getBulkExportUrl,
  importAllTables,
  type DatabaseTableInfo,
  type DatabaseTablesResponse,
  type TableDataResponse,
} from "@/lib/api/admin";
import { useToast } from "@/lib/admin-feedback";

const formatNumber = (n: number) => n.toLocaleString("zh-CN");

function truncateStr(s: string | number | boolean | null | undefined, maxLen = 80): string {
  if (s === null || s === undefined) return "";
  const str = String(s);
  if (str.length <= maxLen) return str;
  return str.substring(0, maxLen) + "…";
}

export default function DatabasePage() {
  const toast = useToast();
  const [csrf, setCsrf] = useState("");
  const [loading, setLoading] = useState(true);
  const [tablesData, setTablesData] = useState<DatabaseTablesResponse | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [tableData, setTableData] = useState<TableDataResponse | null>(null);
  const [tableLoading, setTableLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(50);
  const [importing, setImporting] = useState(false);
  const [bulkImporting, setBulkImporting] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const bulkFileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [csrfRes, tablesRes] = await Promise.all([
          getCsrf(),
          listDatabaseTables(""),
        ]);
        if (cancelled) return;
        setCsrf(csrfRes.csrf_token);
        setTablesData(tablesRes);
        if (tablesRes.tables.length > 0) {
          setSelectedTable(tablesRes.tables[0].name);
        }
      } catch (e: any) {
        toast.error("加载失败：" + (e?.message || "未知错误"));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!selectedTable || !csrf) return;
    let cancelled = false;
    (async () => {
      setTableLoading(true);
      try {
        const data = await getTableData(csrf, selectedTable, page, perPage);
        if (cancelled) return;
        setTableData(data);
      } catch (e: any) {
        toast.error("加载表数据失败：" + (e?.message || "未知错误"));
      } finally {
        if (!cancelled) setTableLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [selectedTable, page, perPage, csrf]);

  const handleExport = (table: string, format: "csv" | "json") => {
    const url = getTableExportUrl(table, format);
    window.open(url, "_blank");
  };

  const handleBulkExport = () => {
    const url = getBulkExportUrl();
    window.open(url, "_blank");
  };

  const handleImportClick = () => {
    fileInputRef.current?.click();
  };

  const handleBulkImportClick = () => {
    bulkFileInputRef.current?.click();
  };

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !selectedTable || !csrf) return;

    if (!file.name.toLowerCase().endsWith(".csv") && !file.name.toLowerCase().endsWith(".json")) {
      toast.error("只支持 CSV 或 JSON 格式");
      return;
    }

    setImporting(true);
    try {
      const result = await importTableData(csrf, selectedTable, file);
      toast.success(result.message || `成功导入 ${result.imported} 条记录`);
      const data = await getTableData(csrf, selectedTable, page, perPage);
      setTableData(data);
      const tables = await listDatabaseTables(csrf);
      setTablesData(tables);
    } catch (e: any) {
      toast.error("导入失败：" + (e?.message || "未知错误"));
    } finally {
      setImporting(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  const handleBulkFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file || !csrf) return;

    if (!file.name.toLowerCase().endsWith(".zip")) {
      toast.error("只支持 ZIP 格式文件");
      return;
    }

    setBulkImporting(true);
    try {
      const result = await importAllTables(csrf, file);
      let msg = result.message || `成功导入 ${result.imported} 条记录`;
      if (result.skipped_tables && result.skipped_tables.length > 0) {
        msg += ` (跳过: ${result.skipped_tables.join(", ")})`;
      }
      toast.success(msg);
      if (selectedTable) {
        const data = await getTableData(csrf, selectedTable, page, perPage);
        setTableData(data);
      }
      const tables = await listDatabaseTables(csrf);
      setTablesData(tables);
    } catch (e: any) {
      toast.error("批量导入失败：" + (e?.message || "未知错误"));
    } finally {
      setBulkImporting(false);
      if (bulkFileInputRef.current) bulkFileInputRef.current.value = "";
    }
  };

  const totalPages = tableData ? Math.ceil(tableData.total / tableData.per_page) : 0;

  if (loading) {
    return <div className="admin-loading">加载中…</div>;
  }

  if (!tablesData) {
    return <div className="admin-error">加载失败</div>;
  }

  const currentTable = tablesData.tables.find(t => t.name === selectedTable);
  const canImport = tablesData.permissions.can_import && currentTable?.can_import;

  return (
    <div className="database-admin">
      <h1 className="admin-page-title">🗄️ 数据库管理</h1>

      <div className="db-perm-info">
        <span className={`perm-badge ${tablesData.permissions.is_owner ? "owner" : tablesData.permissions.is_admin ? "admin" : "user"}`}>
          {tablesData.permissions.is_owner ? "站长权限" : tablesData.permissions.is_admin ? "管理员权限" : "普通用户"}
        </span>
        {tablesData.permissions.can_import && (
          <span className="perm-note">可导入数据</span>
        )}
      </div>

      <div className="db-bulk-actions">
        <button
          className="admin-btn admin-btn-success"
          onClick={handleBulkExport}
        >
          📦 一键导出所有表 (ZIP)
        </button>
        {tablesData.permissions.can_import && (
          <button
            className="admin-btn admin-btn-warning"
            onClick={handleBulkImportClick}
            disabled={bulkImporting}
          >
            {bulkImporting ? "批量导入中…" : "📤 一键导入 ZIP"}
          </button>
        )}
        <input
          ref={bulkFileInputRef}
          type="file"
          accept=".zip"
          style={{ display: "none" }}
          onChange={handleBulkFileChange}
        />
        <span className="db-bulk-hint">
          {tablesData.permissions.is_owner
            ? "导出包含当前可访问表的 CSV 文件压缩包，导入将恢复 ZIP 内所有表数据"
            : tablesData.permissions.is_admin
              ? "导出包含所有表的 CSV 文件压缩包"
              : "导出包含文章表的 CSV 文件压缩包"}
        </span>
      </div>

      <div className="db-layout">
        <aside className="db-tables-list">
          <h3>数据表</h3>
          {tablesData.tables.map(t => (
            <button
              key={t.name}
              className={`db-table-item ${selectedTable === t.name ? "active" : ""}`}
              onClick={() => {
                setSelectedTable(t.name);
                setPage(1);
              }}
            >
              <span className="db-table-name">{t.name}</span>
              <span className="db-table-rows">{formatNumber(t.rows)} 行</span>
            </button>
          ))}
        </aside>

        <main className="db-table-content">
          {selectedTable && tableData && (
            <>
              <div className="db-table-header">
                <h2>{selectedTable}</h2>
                <div className="db-table-actions">
                  <button
                    className="admin-btn admin-btn-secondary"
                    onClick={() => handleExport(selectedTable, "csv")}
                    disabled={tableLoading}
                  >
                    📥 导出 CSV
                  </button>
                  <button
                    className="admin-btn admin-btn-secondary"
                    onClick={() => handleExport(selectedTable, "json")}
                    disabled={tableLoading}
                  >
                    📥 导出 JSON
                  </button>
                  {canImport && (
                    <button
                      className="admin-btn admin-btn-primary"
                      onClick={handleImportClick}
                      disabled={importing || tableLoading}
                    >
                      {importing ? "导入中…" : "📤 导入数据"}
                    </button>
                  )}
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".csv,.json"
                    style={{ display: "none" }}
                    onChange={handleFileChange}
                  />
                </div>
              </div>

              <div className="db-table-info">
                共 <strong>{formatNumber(tableData.total)}</strong> 条记录，
                当前第 <strong>{tableData.page}</strong> / {totalPages} 页
              </div>

              <div className="db-per-page">
                <label>每页显示：</label>
                <select
                  value={perPage}
                  onChange={e => {
                    setPerPage(Number(e.target.value));
                    setPage(1);
                  }}
                >
                  <option value={20}>20</option>
                  <option value={50}>50</option>
                  <option value={100}>100</option>
                  <option value={200}>200</option>
                  <option value={500}>500</option>
                </select>
              </div>

              {tableLoading ? (
                <div className="admin-loading">加载数据中…</div>
              ) : (
                <div className="db-table-wrapper">
                  <table className="db-data-table">
                    <thead>
                      <tr>
                        {tableData.columns.map(col => (
                          <th key={col}>{col}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {tableData.data.length === 0 ? (
                        <tr>
                          <td colSpan={tableData.columns.length} className="db-empty">
                            暂无数据
                          </td>
                        </tr>
                      ) : (
                        tableData.data.map((row, i) => (
                          <tr key={i}>
                            {row.map((cell, j) => (
                              <td key={j} title={String(cell ?? "")}>
                                <code>{truncateStr(cell, 100)}</code>
                              </td>
                            ))}
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              )}

              {totalPages > 1 && (
                <div className="db-pagination">
                  <button
                    className="admin-btn admin-btn-secondary"
                    disabled={page <= 1 || tableLoading}
                    onClick={() => setPage(p => Math.max(1, p - 1))}
                  >
                    ← 上一页
                  </button>
                  <span className="db-page-info">
                    第 {page} / {totalPages} 页
                  </span>
                  <button
                    className="admin-btn admin-btn-secondary"
                    disabled={page >= totalPages || tableLoading}
                    onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                  >
                    下一页 →
                  </button>
                </div>
              )}
            </>
          )}
        </main>
      </div>

      <style jsx>{`
        .database-admin {
          max-width: 100%;
        }
        .admin-page-title {
          font-size: 1.8rem;
          margin: 0 0 1rem 0;
        }
        .db-perm-info {
          margin-bottom: 1.5rem;
          display: flex;
          gap: 0.75rem;
          align-items: center;
        }
        .perm-badge {
          padding: 0.25rem 0.75rem;
          border-radius: 999px;
          font-size: 0.85rem;
          font-weight: 600;
        }
        .perm-badge.owner {
          background: linear-gradient(135deg, #f59e0b, #ef4444);
          color: white;
        }
        .perm-badge.admin {
          background: #3b82f6;
          color: white;
        }
        .perm-badge.user {
          background: #6b7280;
          color: white;
        }
        .perm-note {
          font-size: 0.85rem;
          color: #6b7280;
        }
        .db-bulk-actions {
          margin-bottom: 1.5rem;
          padding: 1rem 1.25rem;
          background: linear-gradient(135deg, #f0fdf4, #eff6ff);
          border-radius: 8px;
          border: 1px solid #bbf7d0;
          display: flex;
          align-items: center;
          gap: 0.75rem;
          flex-wrap: wrap;
        }
        .db-bulk-hint {
          font-size: 0.85rem;
          color: #4b5563;
          margin-left: auto;
        }
        @media (max-width: 768px) {
          .db-bulk-hint {
            margin-left: 0;
            width: 100%;
          }
        }
        .db-layout {
          display: grid;
          grid-template-columns: 240px 1fr;
          gap: 1.5rem;
          min-height: 500px;
        }
        @media (max-width: 768px) {
          .db-layout {
            grid-template-columns: 1fr;
          }
        }
        .db-tables-list {
          background: white;
          border-radius: 8px;
          padding: 1rem;
          box-shadow: 0 1px 3px rgba(0,0,0,0.1);
          max-height: 70vh;
          overflow-y: auto;
        }
        .db-tables-list h3 {
          margin: 0 0 0.75rem 0;
          font-size: 1rem;
          color: #374151;
          padding-bottom: 0.5rem;
          border-bottom: 1px solid #e5e7eb;
        }
        .db-table-item {
          display: flex;
          justify-content: space-between;
          align-items: center;
          width: 100%;
          padding: 0.5rem 0.75rem;
          margin-bottom: 0.25rem;
          border: none;
          background: transparent;
          border-radius: 6px;
          cursor: pointer;
          text-align: left;
          transition: all 0.15s;
          font-size: 0.9rem;
        }
        .db-table-item:hover {
          background: #f3f4f6;
        }
        .db-table-item.active {
          background: #dbeafe;
          color: #1d4ed8;
          font-weight: 600;
        }
        .db-table-name {
          font-family: ui-monospace, SFMono-Regular, monospace;
        }
        .db-table-rows {
          font-size: 0.75rem;
          color: #6b7280;
          background: #f3f4f6;
          padding: 0.125rem 0.5rem;
          border-radius: 999px;
        }
        .db-table-item.active .db-table-rows {
          background: #bfdbfe;
          color: #1e40af;
        }
        .db-table-content {
          background: white;
          border-radius: 8px;
          padding: 1.5rem;
          box-shadow: 0 1px 3px rgba(0,0,0,0.1);
          overflow: hidden;
        }
        .db-table-header {
          display: flex;
          justify-content: space-between;
          align-items: center;
          margin-bottom: 1rem;
          flex-wrap: wrap;
          gap: 0.75rem;
        }
        .db-table-header h2 {
          margin: 0;
          font-family: ui-monospace, SFMono-Regular, monospace;
          font-size: 1.4rem;
          color: #1f2937;
        }
        .db-table-actions {
          display: flex;
          gap: 0.5rem;
          flex-wrap: wrap;
        }
        .db-table-info {
          margin-bottom: 0.75rem;
          color: #6b7280;
          font-size: 0.9rem;
        }
        .db-per-page {
          margin-bottom: 1rem;
          display: flex;
          align-items: center;
          gap: 0.5rem;
          font-size: 0.9rem;
        }
        .db-per-page select {
          padding: 0.375rem 0.5rem;
          border: 1px solid #d1d5db;
          border-radius: 6px;
          background: white;
        }
        .db-table-wrapper {
          overflow-x: auto;
          border: 1px solid #e5e7eb;
          border-radius: 6px;
        }
        .db-data-table {
          width: 100%;
          border-collapse: collapse;
          font-size: 0.85rem;
        }
        .db-data-table th {
          background: #f9fafb;
          padding: 0.625rem 0.75rem;
          text-align: left;
          font-weight: 600;
          color: #374151;
          border-bottom: 2px solid #e5e7eb;
          white-space: nowrap;
          position: sticky;
          top: 0;
        }
        .db-data-table td {
          padding: 0.5rem 0.75rem;
          border-bottom: 1px solid #f3f4f6;
          max-width: 300px;
          overflow: hidden;
          text-overflow: ellipsis;
          white-space: nowrap;
        }
        .db-data-table td code {
          font-family: ui-monospace, SFMono-Regular, monospace;
          font-size: 0.8rem;
          color: #374151;
          background: transparent;
          padding: 0;
        }
        .db-data-table tbody tr:hover {
          background: #f9fafb;
        }
        .db-empty {
          text-align: center;
          padding: 2rem !important;
          color: #9ca3af;
        }
        .db-pagination {
          margin-top: 1rem;
          display: flex;
          justify-content: center;
          align-items: center;
          gap: 1rem;
        }
        .db-page-info {
          color: #6b7280;
          font-size: 0.9rem;
        }
        .admin-loading {
          text-align: center;
          padding: 3rem;
          color: #6b7280;
        }
        .admin-error {
          text-align: center;
          padding: 3rem;
          color: #dc2626;
        }
        .admin-btn {
          padding: 0.5rem 1rem;
          border-radius: 6px;
          border: none;
          font-size: 0.875rem;
          font-weight: 500;
          cursor: pointer;
          transition: all 0.15s;
        }
        .admin-btn:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }
        .admin-btn-primary {
          background: #2563eb;
          color: white;
        }
        .admin-btn-primary:hover:not(:disabled) {
          background: #1d4ed8;
        }
        .admin-btn-secondary {
          background: #f3f4f6;
          color: #374151;
          border: 1px solid #d1d5db;
        }
        .admin-btn-secondary:hover:not(:disabled) {
          background: #e5e7eb;
        }
        .admin-btn-success {
          background: #10b981;
          color: white;
        }
        .admin-btn-success:hover:not(:disabled) {
          background: #059669;
        }
        .admin-btn-warning {
          background: #f59e0b;
          color: white;
        }
        .admin-btn-warning:hover:not(:disabled) {
          background: #d97706;
        }
      `}</style>
    </div>
  );
}
