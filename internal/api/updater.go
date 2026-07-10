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
	"gokych/internal/core/settings"
)

// ── Release source constants ────────────────────────────────────────
// The updater supports two mirror sources, selectable from the admin UI
// (/admin/update). Both ship the same artifact layout (gokych-{GOOS}-{GOARCH}
// + SHA256SUMS), so the rest of the pipeline (download / verify / replace)
// is source-agnostic.
const (
	githubRepo      = "CrossDark/GoKYCH"
	githubLatestFmt = "https://api.github.com/repos/%s/releases/latest"
	githubTagFmt    = "https://api.github.com/repos/%s/releases/tags/v%s"

	gitcodeOwner = "CrossDark"
	gitcodeRepo  = "GoKych" // gitcode repo name is "GoKych" (capital K), NOT "GoKYCH"
	// gitcode v5 API — same path layout as gitea/forgejo.
	gitcodeLatestFmt = "https://gitcode.com/api/v5/repos/%s/%s/releases/latest"
	gitcodeTagFmt    = "https://gitcode.com/api/v5/repos/%s/%s/releases/tags/v%s"

	defaultBinPath     = "/opt/gokych/bin/gokych"
	assetPrefix        = "gokych-"
	assetSumsName      = "SHA256SUMS"
	partFileSuffix     = ".part"
	maxDownloadRetries = 5
)

// Allowed source values (validated on the API boundary).
const (
	updateSourceGithub  = "github"
	updateSourceGitcode = "gitcode"
)

var (
	// Per-source release cache. Switching sources in the admin UI must not
	// serve a stale GitHub release as a GitCode response (or vice versa).
	releaseCacheMu sync.RWMutex
	releaseCache   = map[string]cachedRelease{
		updateSourceGithub:  {},
		updateSourceGitcode: {},
	}
	releaseCacheTTL = 5 * time.Minute
)

type cachedRelease struct {
	rel *ghRelease
	at  time.Time
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"` // github-only
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"` // github: published_at; gitcode falls back to created_at
	CreatedAt   time.Time `json:"created_at"`   // gitcode-only
	HTMLURL     string    `json:"html_url"`     // github-only
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// gcRelease is gitcode.com's v5 response shape — different field names
// than GitHub. We parse into this then convert to the unified ghRelease
// so downstream code (platformAsset / download / verify) stays source-
// agnostic.
//
// Notable differences from GitHub:
//   - no `draft`, `published_at`, `html_url`
//   - `created_at` instead of `published_at`
//   - asset has no `size` (HEAD gitcode API doesn't populate it)
//   - auto-generated source archives (.zip / .tar.gz etc) live in the
//     same `assets[]` array as user uploads, distinguished by
//     `type:"source"` vs `type:"attach"`. We drop the source ones
//     because the SHA256SUMS file ships binary-only entries.
type gcRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Prerelease bool      `json:"prerelease"`
	CreatedAt  time.Time `json:"created_at"`
	HTMLURL    string    `json:"html_url"` // gitcode actually does include this
	Author     struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []gcAsset `json:"assets"`
}

type gcAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Type               string `json:"type"` // "attach" (user upload) | "source" (auto archive)
	Size               int64  `json:"size"`
}

// normalizeGitCodeDownloadURL fixes GitCode's v5 release asset response:
// browser_download_url currently points at https://api.gitcode.com/... which
// returns openresty 404 for anonymous GETs. The same path on gitcode.com
// redirects to file-cdn.gitcode.com with a signed URL and downloads correctly.
func normalizeGitCodeDownloadURL(raw string) string {
	const badPrefix = "https://api.gitcode.com/"
	if strings.HasPrefix(raw, badPrefix) {
		return "https://gitcode.com/" + strings.TrimPrefix(raw, badPrefix)
	}
	return raw
}

// toGhRelease converts a parsed gitcode response into the unified shape.
// Filters out type:"source" archives (auto-generated zip/tar.gz) — only
// type:"attach" entries are user-uploaded binaries / checksums.
func (g *gcRelease) toGhRelease() *ghRelease {
	out := &ghRelease{
		TagName:     g.TagName,
		Name:        g.Name,
		Body:        g.Body,
		Prerelease:  g.Prerelease,
		CreatedAt:   g.CreatedAt,
		PublishedAt: g.CreatedAt, // surfaced as "发布时间" in admin UI; for gitcode it's the release creation time
		HTMLURL:     g.HTMLURL,
	}
	for _, a := range g.Assets {
		if a.Type == "source" {
			continue // skip auto-generated .zip / .tar.gz / .tar.bz2 / .tar
		}
		out.Assets = append(out.Assets, ghAsset{
			Name:               a.Name,
			BrowserDownloadURL: normalizeGitCodeDownloadURL(a.BrowserDownloadURL),
			Size:               a.Size,
		})
	}
	return out
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

// getUpdateSource reads the persisted "update.source" setting, falling
// back to "github" if missing or invalid. Reads via settings.Load so the
// admin can flip it via the /admin/settings endpoint or the dedicated
// /admin/update/source endpoint (both write the same file).
func (s *Server) getUpdateSource() string {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		slog.Warn("updater: settings.Load failed, falling back to github", "err", err)
		return updateSourceGithub
	}
	upd, _ := cfg["update"].(map[string]interface{})
	src, _ := upd["source"].(string)
	switch src {
	case updateSourceGitcode:
		return updateSourceGitcode
	default:
		return updateSourceGithub
	}
}

func (s *Server) setUpdateSource(source string) error {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	upd, _ := cfg["update"].(map[string]interface{})
	if upd == nil {
		upd = map[string]interface{}{}
	}
	upd["source"] = source
	cfg["update"] = upd
	if err := settings.Save(s.DataDir, cfg); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	// Bust the per-source cache so the next /update/check refetches.
	releaseCacheMu.Lock()
	delete(releaseCache, source)
	releaseCacheMu.Unlock()
	return nil
}

// fetchLatestRelease queries the configured source's "latest" endpoint.
func fetchLatestRelease(client *http.Client, source string) (*ghRelease, error) {
	releaseCacheMu.RLock()
	cached := releaseCache[source]
	if cached.rel != nil && time.Since(cached.at) < releaseCacheTTL {
		cpy := *cached.rel
		releaseCacheMu.RUnlock()
		return &cpy, nil
	}
	releaseCacheMu.RUnlock()

	switch source {
	case updateSourceGitcode:
		return fetchLatestReleaseGitCode(client, source)
	default:
		return fetchLatestReleaseGitHub(client, source)
	}
}

func fetchLatestReleaseGitHub(client *http.Client, source string) (*ghRelease, error) {
	url := fmt.Sprintf(githubLatestFmt, githubRepo)
	req, err := http.NewRequest("GET", url, nil)
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
	releaseCache[source] = cachedRelease{rel: &rel, at: time.Now()}
	releaseCacheMu.Unlock()
	return &rel, nil
}

func fetchLatestReleaseGitCode(client *http.Client, source string) (*ghRelease, error) {
	url := fmt.Sprintf(gitcodeLatestFmt, gitcodeOwner, gitcodeRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
	// Anonymous read works for public releases; no token needed.

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitcode api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("gitcode api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel gcRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("gitcode api decode: %w", err)
	}
	if rel.Prerelease {
		return nil, fmt.Errorf("latest release %q is prerelease=true; only stable releases are eligible for auto-update",
			rel.TagName)
	}
	gh := rel.toGhRelease()

	releaseCacheMu.Lock()
	releaseCache[source] = cachedRelease{rel: gh, at: time.Now()}
	releaseCacheMu.Unlock()
	return gh, nil
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
	Source           string `json:"source"` // "github" | "gitcode" — drives the admin UI toggle
	PublishedAt      string `json:"published_at,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
	ReleaseNotes     string `json:"release_notes,omitempty"`
	DownloadSize     int64  `json:"download_size,omitempty"`
	Error            string `json:"error,omitempty"`
}

func (s *Server) checkUpdateHandler(c *gin.Context) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	binPath := detectBinPath()
	source := s.getUpdateSource()

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
		Source:           source,
	}

	client := &http.Client{Timeout: 10 * time.Second}
	rel, err := fetchLatestRelease(client, source)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式无效。"})
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
	source := s.getUpdateSource()

	if ok, reason, _ := canWriteDir(binPath); !ok {
		updater.setErr(fmt.Sprintf("cannot write to %s: %s", filepath.Dir(binPath), reason))
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("无法写入 %s: %s (当前进程以 %s 身份运行)",
			filepath.Dir(binPath), reason, os.Getenv("USER"))})
		return
	}

	// Start the background job and respond immediately.
	updater.mu.Lock()
	updater.status = updateDownloading
	updater.startAt = time.Now()
	updater.mu.Unlock()

	go s.runUpdate(goos, goarch, binPath, req.Version, source)

	c.JSON(http.StatusOK, applyUpdateResponse{
		Success: true,
		Message: "更新任务已启动，正在后台下载...",
	})
}

// setUpdateSourceHandler switches the persisted "update.source" setting
// between "github" and "gitcode". Admin/owner only.
//
// We invalidate the per-source cache so the next /update/check refetches
// instead of returning a stale release from the previous source.
type setUpdateSourceRequest struct {
	Source string `json:"source"`
}

func (s *Server) setUpdateSourceHandler(c *gin.Context) {
	var req setUpdateSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式无效。"})
		return
	}
	switch req.Source {
	case updateSourceGithub, updateSourceGitcode:
		// ok
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的更新源，仅支持 github / gitcode。"})
		return
	}
	if err := s.setUpdateSource(req.Source); err != nil {
		slog.Error("setUpdateSource: save failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存更新源设置失败。"})
		return
	}
	slog.Info("update: source switched", "source", req.Source)
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"source":  req.Source,
		"message": fmt.Sprintf("更新源已切换为 %s,缓存已清除。", req.Source),
	})
}

// runUpdate performs the actual download+verify+replace+restart in a
// background goroutine. It updates the global updater state as it
// progresses so the frontend can poll /update/status.
func (s *Server) runUpdate(goos, goarch, binPath, targetVersion, source string) {
	// Use a long timeout: GitHub releases can be slow to download from
	// China; the binary is ~15-20 MB, so give it 5 minutes.
	client := &http.Client{Timeout: 5 * time.Minute}

	var rel *ghRelease
	var err error

	updater.set(updateDownloading, "正在获取 Release 元数据...")
	if targetVersion != "" {
		rel, err = fetchReleaseByTag(client, source, targetVersion)
	} else {
		rel, err = fetchLatestRelease(client, source)
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

	// Use a deterministic part file name so an interrupted download can be
	// resumed across process restarts. Format: .gokych-update-v{tag}.part
	partPath := filepath.Join(dir, ".gokych-update-"+strings.TrimPrefix(rel.TagName, "v")+partFileSuffix)

	// Clean up stale .part files from previous versions to avoid cluttering
	// the bin directory. Errors are non-fatal.
	cleanupStaleParts(dir, partPath)

	// Open (or create) the part file. If it already exists from a prior
	// interrupted download, downloadToFileWithProgress will send a Range
	// header to resume from the current file size.
	tmp, err := os.OpenFile(partPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		updater.setErr(fmt.Sprintf("创建临时文件失败: %v", err))
		return
	}
	tmpPath := partPath
	success := false
	defer func() {
		if !success {
			// Keep the .part file on failure so the next attempt can resume.
			tmp.Close()
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
				os.Remove(tmpPath)
				updater.setErr(fmt.Sprintf("计算文件哈希失败: %v", herr))
				return
			}
			if !strings.EqualFold(gotHash, wantHash) {
				// Remove the corrupted part file so the next attempt
				// starts from scratch instead of reusing bad data.
				os.Remove(tmpPath)
				updater.setErr(fmt.Sprintf("SHA256 校验不匹配: 文件可能已损坏或被篡改"))
				return
			}
			slog.Info("update: SHA256 verified", "hash", gotHash)
		}
	}

	updater.set(updateReplacing, "正在设置可执行权限...")
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
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

func fetchReleaseByTag(client *http.Client, source, version string) (*ghRelease, error) {
	tag := strings.TrimPrefix(version, "v")
	switch source {
	case updateSourceGitcode:
		url := fmt.Sprintf(gitcodeTagFmt, gitcodeOwner, gitcodeRepo, tag)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gitcode api: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("gitcode api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var rel gcRelease
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return nil, fmt.Errorf("gitcode api decode: %w", err)
		}
		return rel.toGhRelease(), nil
	default:
		url := fmt.Sprintf(githubTagFmt, githubRepo, tag)
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
			return nil, fmt.Errorf("github api decode: %w", err)
		}
		return &rel, nil
	}
}

// cleanupStaleParts removes .part files from previous (different) versions
// in dir. It keeps the current version's part file (keepPath) intact so
// an interrupted download of the in-progress version can still resume.
func cleanupStaleParts(dir, keepPath string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".gokych-update-") || !strings.HasSuffix(name, partFileSuffix) {
			continue
		}
		full := filepath.Join(dir, name)
		if full == keepPath {
			continue
		}
		if err := os.Remove(full); err != nil {
			slog.Warn("update: cannot remove stale part file", "path", full, "err", err)
		} else {
			slog.Info("update: cleaned stale part file", "path", full)
		}
	}
}

// downloadToFileWithProgress downloads url to f with resume support and
// automatic retry on transient network errors.
//
// Resume mechanism:
//  1. Before each HTTP request, stat f to get its current size (existing).
//  2. If existing > 0, send "Range: bytes={existing}-" to ask the server to
//     send only the remaining bytes.
//  3. A 206 Partial Content response means the server honored the range —
//     we append to f starting from offset `existing`.
//  4. A 200 OK response means the server ignored the Range header (rare for
//     GitHub, but possible with proxies) — we truncate f and start over.
//  5. On network error (timeout, connection reset, unexpected EOF), wait
//     with exponential backoff and retry up to maxDownloadRetries times,
//     each time re-checking the file size to resume from where we left off.
func downloadToFileWithProgress(client *http.Client, url string, f *os.File, total int64, job *updateJobState) error {
	var downloaded int64

	for attempt := 0; attempt < maxDownloadRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 8*time.Second {
				backoff = 8 * time.Second
			}
			job.set(updateDownloading, fmt.Sprintf("网络中断，%v 后重试（第 %d/%d 次）...", backoff, attempt+1, maxDownloadRetries))
			slog.Warn("update: download retry", "attempt", attempt+1, "backoff", backoff, "downloaded", downloaded)
			time.Sleep(backoff)
		}

		// Determine current file size to decide whether to send Range.
		st, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat part file: %w", err)
		}
		existing := st.Size()

		// If the file is already complete (e.g. previous attempt finished
		// the download but failed before verification), skip the download.
		if total > 0 && existing >= total {
			job.setProgress(existing, total)
			downloaded = existing
			return nil
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "GoKYCH-SelfUpdater")
		if existing > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
		}

		resp, err := client.Do(req)
		if err != nil {
			// Network-level error (DNS, timeout, connection reset) → retry.
			downloaded = existing
			job.setProgress(downloaded, total)
			if attempt == maxDownloadRetries-1 {
				return fmt.Errorf("download request failed after %d retries: %w", maxDownloadRetries, err)
			}
			continue
		}

		// Handle 416 Range Not Satisfiable: the server says our range is
		// beyond the file. This means the part file is larger than the
		// remote asset (different version?). Truncate and start over.
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			resp.Body.Close()
			slog.Warn("update: range not satisfiable, truncating part file", "existing", existing)
			if err := f.Truncate(0); err != nil {
				return fmt.Errorf("truncate part file: %w", err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("seek part file: %w", err)
			}
			downloaded = 0
			continue
		}

		// If the server ignored our Range request and returned 200 with the
		// full body, we must truncate and write from the beginning.
		if existing > 0 && resp.StatusCode == http.StatusOK {
			slog.Info("update: server returned 200 instead of 206, restarting from beginning")
			if err := f.Truncate(0); err != nil {
				resp.Body.Close()
				return fmt.Errorf("truncate part file: %w", err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				resp.Body.Close()
				return fmt.Errorf("seek part file: %w", err)
			}
			existing = 0
		} else if resp.StatusCode == http.StatusPartialContent {
			// Server honored our Range request; seek to end to append.
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				resp.Body.Close()
				return fmt.Errorf("seek part file end: %w", err)
			}
		} else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return fmt.Errorf("download returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		// Determine the expected total size from Content-Range or Content-Length.
		if total <= 0 {
			if cr := resp.Header.Get("Content-Range"); cr != "" {
				// Content-Range: bytes 100-199/200 → total is after '/'
				if idx := strings.LastIndex(cr, "/"); idx >= 0 {
					fmt.Sscanf(cr[idx+1:], "%d", &total)
				}
			}
			if total <= 0 && resp.ContentLength > 0 {
				total = resp.ContentLength + existing
			}
		}

		// Stream the body to disk.
		downloaded = existing
		buf := make([]byte, 64*1024)
		lastReport := time.Now()
		readErr := error(nil)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := f.Write(buf[:n]); werr != nil {
					resp.Body.Close()
					return werr
				}
				downloaded += int64(n)
				if time.Since(lastReport) > 250*time.Millisecond {
					job.setProgress(downloaded, total)
					lastReport = time.Now()
				}
			}
			if rerr == io.EOF {
				readErr = nil
				break
			}
			if rerr != nil {
				readErr = rerr
				break
			}
		}
		resp.Body.Close()

		if readErr == nil {
			// Download completed successfully for this attempt.
			// Sync to disk before returning so a crash doesn't lose data.
			f.Sync()
			job.setProgress(downloaded, total)
			return nil
		}

		// Transient read error (connection reset, timeout, etc.) → retry.
		slog.Warn("update: read error during download, will retry",
			"err", readErr, "downloaded", downloaded, "total", total, "attempt", attempt+1)
	}

	return fmt.Errorf("download failed after %d retries; partial file kept for manual resume", maxDownloadRetries)
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
			name = strings.TrimPrefix(name, "./")
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
