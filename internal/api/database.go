package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/user"
)

var allowedTables = []string{
	"users", "articles", "tags", "article_tags", "comments", "ratings",
	"webauthn_credentials", "subsite_links", "featured_articles", "notifications",
	"static_files", "typst_files", "typst_cache", "api_keys", "typst_compile_queue",
	"article_deps", "sidebar_cards", "article_revisions", "theme_settings",
}

var userVisibleTables = map[string]bool{
	"articles": true,
}

func isTableAllowed(table string) bool {
	for _, t := range allowedTables {
		if t == table {
			return true
		}
	}
	return false
}

func canAccessTable(u *user.User, table string) bool {
	if u == nil {
		return false
	}
	if user.IsOwner(u.Role) {
		return isTableAllowed(table)
	}
	if user.IsAdmin(u.Role) {
		return isTableAllowed(table)
	}
	return userVisibleTables[table]
}

func canImport(u *user.User) bool {
	return u != nil && user.IsOwner(u.Role)
}

func getAccessibleTables(u *user.User) []string {
	tables := []string{}
	if u == nil {
		return tables
	}
	if user.IsAdmin(u.Role) {
		return append(tables, allowedTables...)
	}
	for t := range userVisibleTables {
		if userVisibleTables[t] {
			tables = append(tables, t)
		}
	}
	return tables
}

func (s *Server) exportAllTables(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}

	tables := getAccessibleTables(u)
	if len(tables) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "没有可导出的表。"})
		return
	}

	ctx := c.Request.Context()
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	for _, tableName := range tables {
		columns, err := getTableColumns(ctx, s.DB, tableName)
		if err != nil {
			slog.Warn("get columns failed for bulk export", "table", tableName, "err", err)
			continue
		}

		colList := "`" + strings.Join(columns, "`, `") + "`"
		query := fmt.Sprintf("SELECT %s FROM `%s`", colList, tableName)
		rows, err := s.DB.QueryContext(ctx, query)
		if err != nil {
			slog.Warn("query failed for bulk export", "table", tableName, "err", err)
			continue
		}

		f, err := w.Create(tableName + ".csv")
		if err != nil {
			rows.Close()
			slog.Warn("create zip entry failed", "table", tableName, "err", err)
			continue
		}

		f.Write([]byte{0xEF, 0xBB, 0xBF})
		csvW := csv.NewWriter(f)
		_ = csvW.Write(columns)

		colTypes, _ := rows.ColumnTypes()
		for rows.Next() {
			values := make([]interface{}, len(columns))
			scanArgs := make([]interface{}, len(columns))
			for i := range values {
				scanArgs[i] = &values[i]
			}
			if err := rows.Scan(scanArgs...); err != nil {
				slog.Warn("scan row failed", "err", err)
				continue
			}
			strs := make([]string, len(columns))
			for i, v := range values {
				converted := convertValue(v, colTypes[i])
				strs[i] = fmt.Sprintf("%v", converted)
			}
			_ = csvW.Write(strs)
		}
		csvW.Flush()
		rows.Close()
	}

	if err := w.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建ZIP失败。"})
		return
	}

	filename := fmt.Sprintf("gokych-db-export-%s.zip", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

func (s *Server) importAllTables(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	if !canImport(u) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有站长可以导入数据。"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传ZIP文件。"})
		return
	}
	defer file.Close()

	if header.Size > 200<<20 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件不能超过 200MB。"})
		return
	}

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只支持 ZIP 格式文件。"})
		return
	}

	zipBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败。"})
		return
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ZIP 文件解析失败: " + err.Error()})
		return
	}

	ctx := c.Request.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "事务启动失败。"})
		return
	}
	defer tx.Rollback()

	totalImported := 0
	importedTables := map[string]int{}
	skippedTables := []string{}

	for _, zf := range zipReader.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := zf.Name
		if !strings.HasSuffix(strings.ToLower(name), ".csv") {
			continue
		}
		tableName := strings.TrimSuffix(name, ".csv")
		if !isTableAllowed(tableName) {
			skippedTables = append(skippedTables, tableName)
			slog.Warn("skipping disallowed table in bulk import", "table", tableName)
			continue
		}

		columns, err := getTableColumns(ctx, s.DB, tableName)
		if err != nil {
			skippedTables = append(skippedTables, tableName)
			slog.Warn("get columns failed for bulk import", "table", tableName, "err", err)
			continue
		}

		rc, err := zf.Open()
		if err != nil {
			slog.Warn("open zip entry failed", "table", tableName, "err", err)
			continue
		}

		r := csv.NewReader(rc)
		r.FieldsPerRecord = -1
		allRows, err := r.ReadAll()
		rc.Close()
		if err != nil {
			slog.Warn("CSV parse failed for bulk import", "table", tableName, "err", err)
			continue
		}

		if len(allRows) < 1 {
			continue
		}

		headerCols := allRows[0]
		if len(headerCols) > 0 {
			headerCols[0] = stripBOM(headerCols[0])
		}
		colMap := map[string]int{}
		for i, h := range headerCols {
			colMap[strings.TrimSpace(strings.ToLower(h))] = i
		}

		validCols := []string{}
		for _, col := range columns {
			if _, ok := colMap[strings.ToLower(col)]; ok {
				validCols = append(validCols, col)
			}
		}

		if len(validCols) == 0 {
			continue
		}

		colList := "`" + strings.Join(validCols, "`, `") + "`"
		phs := make([]string, len(validCols))
		for i := range phs {
			phs[i] = "?"
		}
		q := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", tableName, colList, strings.Join(phs, ", "))

		tableImported := 0
		for i, row := range allRows[1:] {
			vals := make([]interface{}, len(validCols))
			hasValue := false
			for j, col := range validCols {
				idx := colMap[strings.ToLower(col)]
				if idx < len(row) {
					vals[j] = strings.TrimSpace(row[idx])
					if vals[j] != "" {
						hasValue = true
					}
				}
			}
			if !hasValue {
				continue
			}
			if _, err := tx.ExecContext(ctx, q, vals...); err != nil {
				slog.Warn("bulk import row failed", "table", tableName, "row", i+2, "err", err)
				continue
			}
			tableImported++
			totalImported++
		}
		importedTables[tableName] = tableImported
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交失败。"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":          "ok",
		"imported":        totalImported,
		"imported_tables": importedTables,
		"skipped_tables":  skippedTables,
		"message":         fmt.Sprintf("成功导入 %d 条记录，涉及 %d 个表。", totalImported, len(importedTables)),
	})
}

func (s *Server) listDatabaseTables(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}

	tables := getAccessibleTables(u)

	type tableInfo struct {
		Name       string `json:"name"`
		Rows       int64  `json:"rows"`
		CanImport  bool   `json:"can_import"`
		CanExport  bool   `json:"can_export"`
	}

	result := []tableInfo{}
	ctx := c.Request.Context()
	for _, t := range tables {
		var cnt int64
		if err := s.DB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`", t)).Scan(&cnt); err != nil {
			slog.Warn("count table failed", "table", t, "err", err)
			continue
		}
		result = append(result, tableInfo{
			Name:      t,
			Rows:      cnt,
			CanImport: canImport(u),
			CanExport: true,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"tables": result,
		"permissions": gin.H{
			"can_import": canImport(u),
			"is_admin":   user.IsAdmin(u.Role),
			"is_owner":   user.IsOwner(u.Role),
		},
	})
}

func (s *Server) getTableData(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}

	tableName := c.Param("table")
	if !canAccessTable(u, tableName) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此表。"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	ctx := c.Request.Context()

	columns, err := getTableColumns(ctx, s.DB, tableName)
	if err != nil {
		slog.Error("get columns failed", "table", tableName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取表结构失败。"})
		return
	}

	var total int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tableName)
	if err := s.DB.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		slog.Error("count failed", "table", tableName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据失败。"})
		return
	}

	colList := "`" + strings.Join(columns, "`, `") + "`"
	query := fmt.Sprintf("SELECT %s FROM `%s` ORDER BY 1 DESC LIMIT ? OFFSET ?", colList, tableName)
	rows, err := s.DB.QueryContext(ctx, query, pageSize, offset)
	if err != nil {
		slog.Error("query failed", "table", tableName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据失败。"})
		return
	}
	defer rows.Close()

	data := [][]interface{}{}
	colTypes, _ := rows.ColumnTypes()
	for rows.Next() {
		values := make([]interface{}, len(columns))
		scanArgs := make([]interface{}, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			slog.Warn("scan row failed", "err", err)
			continue
		}
		row := make([]interface{}, len(columns))
		for i, v := range values {
			row[i] = convertValue(v, colTypes[i])
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		slog.Error("iterate rows failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取数据失败。"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"columns":  columns,
		"data":     data,
		"total":    total,
		"page":     page,
		"per_page": pageSize,
	})
}

func (s *Server) exportTable(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}

	tableName := c.Param("table")
	if !canAccessTable(u, tableName) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此表。"})
		return
	}

	format := c.DefaultQuery("format", "csv")

	ctx := c.Request.Context()
	columns, err := getTableColumns(ctx, s.DB, tableName)
	if err != nil {
		slog.Error("get columns failed", "table", tableName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取表结构失败。"})
		return
	}

	colList := "`" + strings.Join(columns, "`, `") + "`"
	query := fmt.Sprintf("SELECT %s FROM `%s`", colList, tableName)
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		slog.Error("query failed", "table", tableName, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据失败。"})
		return
	}
	defer rows.Close()

	colTypes, _ := rows.ColumnTypes()
	allData := [][]interface{}{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		scanArgs := make([]interface{}, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			slog.Warn("scan row failed", "err", err)
			continue
		}
		row := make([]interface{}, len(columns))
		for i, v := range values {
			row[i] = convertValue(v, colTypes[i])
		}
		allData = append(allData, row)
	}

	if format == "json" {
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, tableName))
		jsonRows := []map[string]interface{}{}
		for _, row := range allData {
			m := map[string]interface{}{}
			for i, col := range columns {
				m[col] = row[i]
			}
			jsonRows = append(jsonRows, m)
		}
		enc := json.NewEncoder(c.Writer)
		enc.SetIndent("", "  ")
		_ = enc.Encode(jsonRows)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, tableName))
	c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(c.Writer)
	_ = w.Write(columns)
	for _, row := range allData {
		strs := make([]string, len(row))
		for i, v := range row {
			strs[i] = fmt.Sprintf("%v", v)
		}
		_ = w.Write(strs)
	}
	w.Flush()
}

func (s *Server) importTable(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	if !canImport(u) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有站长可以导入数据。"})
		return
	}

	tableName := c.Param("table")
	if !isTableAllowed(tableName) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不允许导入到此表。"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件。"})
		return
	}
	defer file.Close()

	if header.Size > 50<<20 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件不能超过 50MB。"})
		return
	}

	ctx := c.Request.Context()
	columns, err := getTableColumns(ctx, s.DB, tableName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取表结构失败。"})
		return
	}

	var rows [][]string
	if strings.HasSuffix(strings.ToLower(header.Filename), ".json") {
		var data []map[string]interface{}
		if err := json.NewDecoder(file).Decode(&data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 解析失败: " + err.Error()})
			return
		}
		rows = make([][]string, 0, len(data))
		rows = append(rows, columns)
		for _, item := range data {
			row := make([]string, len(columns))
			for i, col := range columns {
				if v, ok := item[col]; ok {
					row[i] = fmt.Sprintf("%v", v)
				}
			}
			rows = append(rows, row)
		}
	} else {
		r := csv.NewReader(file)
		r.FieldsPerRecord = -1
		allRows, err := r.ReadAll()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CSV 解析失败: " + err.Error()})
			return
		}
		rows = allRows
	}

	if len(rows) < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件为空。"})
		return
	}

	headerCols := rows[0]
	if len(headerCols) > 0 {
		headerCols[0] = stripBOM(headerCols[0])
	}
	colMap := map[string]int{}
	for i, h := range headerCols {
		colMap[strings.TrimSpace(strings.ToLower(h))] = i
	}

	validCols := []string{}
	for _, col := range columns {
		if idx, ok := colMap[strings.ToLower(col)]; ok {
			validCols = append(validCols, col)
			_ = idx
		}
	}

	if len(validCols) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有找到匹配的列。"})
		return
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "事务启动失败。"})
		return
	}
	defer tx.Rollback()

	colList := "`" + strings.Join(validCols, "`, `") + "`"
	imported := 0
	for i, row := range rows[1:] {
		vals := make([]interface{}, len(validCols))
		phs := make([]string, len(validCols))
		hasValue := false
		for j, col := range validCols {
			idx := colMap[strings.ToLower(col)]
			if idx < len(row) {
				vals[j] = strings.TrimSpace(row[idx])
				if vals[j] != "" {
					hasValue = true
				}
			}
			phs[j] = "?"
		}
		if !hasValue {
			continue
		}
		q := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", tableName, colList, strings.Join(phs, ", "))
		if _, err := tx.ExecContext(ctx, q, vals...); err != nil {
			slog.Warn("import row failed", "row", i+2, "err", err)
			continue
		}
		imported++
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交失败。"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"imported": imported,
		"message":  fmt.Sprintf("成功导入 %d 条记录。", imported),
	})
}

func getTableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SHOW COLUMNS FROM `"+table+"`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := []string{}
	for rows.Next() {
		var field, colType string
		var null, key, extra sql.NullString
		var def sql.NullString
		if err := rows.Scan(&field, &colType, &null, &key, &def, &extra); err != nil {
			continue
		}
		cols = append(cols, field)
	}
	return cols, rows.Err()
}

func convertValue(v interface{}, ct *sql.ColumnType) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	default:
		return val
	}
}

func stripBOM(s string) string {
	if strings.HasPrefix(s, "\xEF\xBB\xBF") {
		return s[3:]
	}
	return s
}
