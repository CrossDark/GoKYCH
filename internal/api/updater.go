package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── GitHub release constants ─────────────────────────────────────────
const (
	githubRepo     = "CrossDark/GoKYCH"
	githubAPIURL   = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	defaultBinPath = "/opt/gokych/bin/gokych"
	assetPrefix    = "gokych-"
	assetSumsName  = "SHA256SUMS"
)

var (
	releaseCacheMu  sync.RWMutex
	releaseCache    *ghRelease
	releaseCacheAt  time.Time
	releaseCacheTTL = 5 * time.Minute
)

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func (r *ghRelease) platformAsset(goos, goarch string) (binURL, sumsURL string) {
	want := assetPrefix + goos + "-" + goarch
	for _, a := range r.Assets {
		if a.Name == want {
			binURL = a.BrowserDownloadURL
		}
		if a.Name == assetSumsName {
			sumsURL = a.BrowserDownloadURL
		}
	}
	return binURL, sumsURL
}

func fetchLatestRelease(client *http.Client) (*ghRelease, error) {
	releaseCacheMu.RLock()
	if releaseCache != nil && time.Since(releaseCacheAt) < releaseCacheTTL {
		cached := *releaseCache
		releaseCacheMu.RUnlock()
		return &cached, nil
	}
	releaseCacheMu.RUnlock()

	req, err := http.NewRequest("GET", githubAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github api decode: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		return nil, fmt.Errorf("latest release %q is draft=%v prerelease=%v; only stable releases are eligible for auto-update",
			rel.TagName, rel.Draft, rel.Prerelease)
	}

	releaseCacheMu.Lock()
	releaseCache = &rel
	releaseCacheAt = time.Now()
	releaseCacheMu.Unlock()
	return &rel, nil
}

func detectBinPath() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
			return real
		}
		return exe
	}
	return defaultBinPath
}

func canWriteDir(path string) (bool, string, string) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gokych-write-test-")
	if err != nil {
		return false, err.Error(), classifyWriteErr(err)
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return true, "", ""
}

func classifyWriteErr(err error) string {
	if err == nil {
		return ""
	}
	if isEROFS(err) {
		return "erofs"
	}
	if os.IsPermission(err) {
		return "eacces"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "read-only"):
		return "erofs"
	case strings.Contains(msg, "permission denied"):
		return "eacces"
	case strings.Contains(msg, "operation not permitted"):
		return "eperm"
	default:
		return "other"
	}
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

func mountOptionsForPath(path string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	best := ""
	bestOpts := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mp := fields[1]
		opts := fields[3]
		if strings.HasPrefix(abs, mp) && len(mp) > len(best) {
			best = mp
			bestOpts = opts
		}
	}
	return bestOpts
}

func compareVersions(current, latest string) bool {
	norm := func(s string) []int {
		s = strings.TrimPrefix(s, "v")
		parts := strings.Split(s, ".")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			n := 0
			for _, ch := range p {
				if ch >= '0' && ch <= '9' {
					n = n*10 + int(ch-'0')
				} else {
					if len(out) == 0 {
						return nil
					}
					break
				}
			}
			out = append(out, n)
		}
		return out
	}
	c := norm(current)
	l := norm(latest)
	if c == nil || l == nil {
		return (current == "dev" || current == "") && len(l) > 0
	}
	for i := 0; i < len(c) || i < len(l); i++ {
		var ci, li int
		if i < len(c) {
			ci = c[i]
		}
		if i < len(l) {
			li = l[i]
		}
		if li > ci {
			return true
		}
		if li < ci {
			return false
		}
	}
	return false
}

// ── Async update job state ───────────────────────────────────────────
//
// The update runs in a background goroutine so the HTTP response isn't
// blocked by a slow GitHub download (which can take minutes in China).
// A single global job slot is sufficient — we never run two concurrent
// self-updates.

type updateStatus string

const (
	updateIdle        updateStatus = "idle"
	updateDownloading updateStatus = "downloading"
	updateVerifying   updateStatus = "verifying"
	updateReplacing   updateStatus = "replacing"
	updateRestarting  updateStatus = "restarting"
	updateDone        updateStatus = "done"
	updateError       updateStatus = "error"
)

type updateJobState struct {
	mu       sync.RWMutex
	status   updateStatus
	version  string
	startAt  time.Time
	message  string
	error    string
	backup   string
	progress int64
	total    int64
}

func (j *updateJobState) set(s updateStatus, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = s
	j.message = msg
	slog.Info("update: "+msg, "status", s, "version", j.version)
}

func (j *updateJobState) setErr(err string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = updateError
	j.error = err
	slog.Error("update failed", "err", err, "version", j.version)
}

func (j *updateJobState) setProgress(downloaded, total int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = downloaded
	j.total = total
}

func (j *updateJobState) snapshot() (status updateStatus, version, message, errStr, backup string, progress, total int64, elapsedSec float64) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	elapsed := time.Since(j.startAt).Seconds()
	return j.status, j.version, j.message, j.error, j.backup, j.progress, j.total, elapsed
}

var updater = &updateJobState{status: updateIdle}

// ── Handlers ──────────────────────────────────────────────────────────

type updateCheckResponse struct {
	CurrentVersion   string `json:"current_version"`
	LatestVersion    string `json:"latest_version"`
	UpdateAvailable  bool   `json:"update_available"`
	Platform         string `json:"platform"`
	Arch             string `json:"arch"`
	OS               string `json:"os"`
	BinaryPath       string `json:"binary_path"`
	CanWrite         bool   `json:"can_write"`
	CanWriteError    string `json:"can_write_error,omitempty"`
	WriteErrCategory string `json:"write_err_category,omitempty"`
	ProcessUser      string `json:"process_user,omitempty"`
	DirPermissions   string `json:"dir_permissions,omitempty"`
	InContainer      bool   `json:"in_container"`
	MountOptions     string `json:"mount_options,omitempty"`
	PublishedAt      string `json:"published_at,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
	ReleaseNotes     string `json:"release_notes,omitempty"`
	DownloadSize     int64  `json:"download_size,omitempty"`
	Error            string `json:"error,omitempty"`
}

func (s *Server) checkUpdateHandler(c *gin.Context) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	binPath := detectBinPath()

	canW, canWErr, errCategory := canWriteDir(binPath)

	dir := filepath.Dir(binPath)
	var dirPerms string
	if fi, err := os.Stat(dir); err == nil {
		dirPerms = fmt.Sprintf("%04o", fi.Mode().Perm())
	}

	procUser := os.Getenv("USER")
	if procUser == "" {
		procUser = os.Getenv("LOGNAME")
	}
	if procUser == "" {
		procUser = fmt.Sprintf("pid=%d", os.Getpid())
	}

	resp := updateCheckResponse{
		CurrentVersion:   s.Version,
		Platform:         goos + "/" + goarch,
		OS:               goos,
		Arch:             goarch,
		BinaryPath:       binPath,
		CanWrite:         canW,
		CanWriteError:    canWErr,
		WriteErrCategory: errCategory,
		ProcessUser:      procUser,
		DirPermissions:   dirPerms,
		InContainer:      inContainer(),
		MountOptions:     mountOptionsForPath(binPath),
	}

	client := &http.Client{Timeout: 10 * time.Second}
	rel, err := fetchLatestRelease(client)
	if err != nil {
		resp.Error = err.Error()
		c.JSON(http.StatusOK, resp)
		return
	}

	resp.LatestVersion = rel.TagName
	resp.PublishedAt = rel.PublishedAt.Format(time.RFC3339)
	resp.ReleaseURL = rel.HTMLURL
	resp.ReleaseNotes = rel.Body
	resp.UpdateAvailable = compareVersions(s.Version, rel.TagName)

	binURL, _ := rel.platformAsset(goos, goarch)
	for _, a := range rel.Assets {
		if a.BrowserDownloadURL == binURL {
			resp.DownloadSize = a.Size
			break
		}
	}
	if binURL == "" {
		resp.Error = fmt.Sprintf("latest release %s has no asset for platform %s/%s",
			rel.TagName, goos, goarch)
	}

	c.JSON(http.StatusOK, resp)
}

type applyUpdateRequest struct {
	Version string `json:"version"`
}

type applyUpdateResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (s *Server) applyUpdateHandler(c *gin.Context) {
	var req applyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	updater.mu.Lock()
	if updater.status != updateIdle && updater.status != updateDone && updater.status != updateError {
		msg := fmt.Sprintf("已有更新任务正在进行中（状态: %s）", updater.status)
		updater.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": msg})
		return
	}
	updater.status = updateIdle
	updater.version = ""
	updater.error = ""
	updater.message = ""
	updater.backup = ""
	updater.progress = 0
	updater.total = 0
	updater.mu.Unlock()

	goos, goarch := runtime.GOOS, runtime.GOARCH
	binPath := detectBinPath()

	if ok, reason, _ := canWriteDir(binPath); !ok {
		updater.setErr(fmt.Sprintf("cannot write to %s: %s", filepath.Dir(binPath), reason))
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("cannot write to %s: %s (process running as %s)",
			filepath.Dir(binPath), reason, os.Getenv("USER"))})
		return
	}

	// Start the background job and respond immediately.
	updater.mu.Lock()
	updater.status = updateDownloading
	updater.startAt = time.Now()
	updater.mu.Unlock()

	go s.runUpdate(goos, goarch, binPath, req.Version)

	c.JSON(http.StatusOK, applyUpdateResponse{
		Success: true,
		Message: "更新任务已启动，正在后台下载...",
	})
}

// runUpdate performs the actual download+verify+replace+restart in a
// background goroutine. It updates the global updater state as it
// progresses so the frontend can poll /update/status.
func (s *Server) runUpdate(goos, goarch, binPath, targetVersion string) {
	// Use a long timeout: GitHub releases can be slow to download from
	// China; the binary is ~15-20 MB, so give it 5 minutes.
	client := &http.Client{Timeout: 5 * time.Minute}

	var rel *ghRelease
	var err error

	updater.set(updateDownloading, "正在获取 Release 元数据...")
	if targetVersion != "" {
		tag := strings.TrimPrefix(targetVersion, "v")
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/v%s", githubRepo, tag)
		r, e := fetchReleaseByTag(client, url)
		rel, err = r, e
	} else {
		rel, err = fetchLatestRelease(client)
	}
	if err != nil {
		updater.setErr(fmt.Sprintf("获取 Release 信息失败: %v", err))
		return
	}

	updater.mu.Lock()
	updater.version = rel.TagName
	updater.mu.Unlock()

	binURL, sumsURL := rel.platformAsset(goos, goarch)
	if binURL == "" {
		updater.setErr(fmt.Sprintf("Release %s 没有平台 %s/%s 的二进制文件", rel.TagName, goos, goarch))
		return
	}

	var totalSize int64
	for _, a := range rel.Assets {
		if a.BrowserDownloadURL == binURL {
			totalSize = a.Size
			break
		}
	}

	dir := filepath.Dir(binPath)
	tmp, err := os.CreateTemp(dir, ".gokych-update-")
	if err != nil {
		updater.setErr(fmt.Sprintf("创建临时文件失败: %v", err))
		return
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	updater.set(updateDownloading, fmt.Sprintf("正在下载 %s ...", rel.TagName))
	slog.Info("update: downloading binary", "version", rel.TagName, "url", binURL, "to", tmpPath)
	if err := downloadToFileWithProgress(client, binURL, tmp, totalSize, updater); err != nil {
		tmp.Close()
		updater.setErr(fmt.Sprintf("下载失败: %v", err))
		return
	}
	tmp.Close()

	updater.set(updateVerifying, "正在校验 SHA256...")
	if sumsURL != "" {
		wantHash, hashErr := fetchExpectedHash(client, sumsURL, filepath.Base(binURL))
		if hashErr != nil {
			slog.Warn("update: cannot fetch SHA256SUMS, skipping verification", "err", hashErr)
		} else if wantHash != "" {
			gotHash, herr := fileSHA256(tmpPath)
			if herr != nil {
				updater.setErr(fmt.Sprintf("计算文件哈希失败: %v", herr))
				return
			}
			if !strings.EqualFold(gotHash, wantHash) {
				updater.setErr(fmt.Sprintf("SHA256 校验不匹配: 文件可能已损坏或被篡改"))
				return
			}
			slog.Info("update: SHA256 verified", "hash", gotHash)
		}
	}

	updater.set(updateReplacing, "正在设置可执行权限...")
	if err := os.Chmod(tmpPath, 0755); err != nil {
		updater.setErr(fmt.Sprintf("chmod 失败: %v", err))
		return
	}

	backupPath := binPath + ".prev"
	updater.set(updateReplacing, "正在备份当前版本...")
	if err := os.Rename(binPath, backupPath); err != nil {
		slog.Warn("update: cannot rename current binary for backup, trying copy", "err", err)
		if src, err2 := os.Open(binPath); err2 == nil {
			if dst, err3 := os.Create(backupPath); err3 == nil {
				_, _ = io.Copy(dst, src)
				dst.Close()
				os.Chmod(backupPath, 0755)
			}
			src.Close()
		}
	} else {
		slog.Info("update: backed up old binary", "backup", backupPath)
	}

	updater.mu.Lock()
	updater.backup = backupPath
	updater.mu.Unlock()

	updater.set(updateReplacing, "正在替换二进制文件...")
	if err := os.Rename(tmpPath, binPath); err != nil {
		_ = os.Rename(backupPath, binPath)
		updater.setErr(fmt.Sprintf("替换二进制失败: %v", err))
		return
	}
	success = true

	slog.Info("update: binary replaced successfully",
		"old", backupPath, "new", binPath, "version", rel.TagName)

	updater.set(updateRestarting, "正在重启服务...")

	if restartFn != nil {
		go func() {
			time.Sleep(1 * time.Second)
			if err := restartFn(); err != nil {
				slog.Error("update: restart function returned error", "err", err)
			}
		}()
	}

	updater.mu.Lock()
	updater.status = updateDone
	updater.message = fmt.Sprintf("已更新到 %s，服务正在重启", rel.TagName)
	updater.mu.Unlock()
}

type updateStatusResponse struct {
	Status     string  `json:"status"`
	Version    string  `json:"version,omitempty"`
	Message    string  `json:"message"`
	Error      string  `json:"error,omitempty"`
	Backup     string  `json:"backup,omitempty"`
	Progress   int64   `json:"progress"`
	Total      int64   `json:"total"`
	ElapsedSec float64 `json:"elapsed_sec"`
}

func (s *Server) updateStatusHandler(c *gin.Context) {
	status, version, message, errStr, backup, progress, total, elapsed := updater.snapshot()
	c.JSON(http.StatusOK, updateStatusResponse{
		Status:     string(status),
		Version:    version,
		Message:    message,
		Error:      errStr,
		Backup:     backup,
		Progress:   progress,
		Total:      total,
		ElapsedSec: elapsed,
	})
}

// ── HTTP helpers ──────────────────────────────────────────────────────

func fetchReleaseByTag(client *http.Client, url string) (*ghRelease, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// downloadToFileWithProgress downloads url to f, reporting byte counts
// into job periodically (every ~256KB) so the frontend can show a bar.
func downloadToFileWithProgress(client *http.Client, url string, f *os.File, total int64, job *updateJobState) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
	// Follow redirects explicitly? http.Client follows up to 10 by default;
	// GitHub release assets redirect to objects.githubusercontent.com.
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Use the Content-Length from the final response if we didn't get a
	// size from the asset metadata.
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	var downloaded int64
	buf := make([]byte, 64*1024)
	lastReport := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			downloaded += int64(n)
			// Throttle progress updates to ~4/sec to avoid mutex churn.
			if time.Since(lastReport) > 250*time.Millisecond {
				job.setProgress(downloaded, total)
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	job.setProgress(downloaded, total)
	return nil
}

func downloadToFile(client *http.Client, url string, f *os.File) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func fetchExpectedHash(client *http.Client, sumsURL, assetName string) (string, error) {
	req, err := http.NewRequest("GET", sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA256SUMS returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) == 2 {
			hash := parts[0]
			name := strings.TrimPrefix(parts[1], "*")
			if name == assetName {
				return hash, nil
			}
		}
	}
	return "", nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
