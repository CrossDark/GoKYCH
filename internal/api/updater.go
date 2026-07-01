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
//
// Asset naming produced by scripts/build-release.sh:
//
//	gokych-linux-amd64
//	gokych-linux-arm64
//	gokych-darwin-amd64
//	gokych-darwin-arm64
//
// plus a SHA256SUMS file in the same release listing "<hex>  gokych-<os>-<arch>".

const (
	githubRepo     = "CrossDark/GoKYCH"
	githubAPIURL   = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	defaultBinPath = "/opt/gokych/bin/gokych"
	assetPrefix    = "gokych-"
	assetSumsName  = "SHA256SUMS"
)

// cache the last GitHub API response for 5 minutes to avoid hitting the
// unauthenticated rate limit (60/hour) when an admin mashes the
// "check update" button.
var (
	releaseCacheMu sync.RWMutex
	releaseCache   *ghRelease
	releaseCacheAt time.Time
	releaseCacheTTL = 5 * time.Minute
)

// ghRelease is a minimal subset of the GitHub Release API response.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL    string    `json:"html_url"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// platformAsset returns the asset matching the current GOOS/GOARCH, plus
// the SHA256SUMS asset if present. Returns ("", "") when no match.
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

// fetchLatestRelease hits the GitHub API (or returns a cached result).
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
	// Skip drafts/prereleases — only promote stable releases.
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

// detectBinPath returns the absolute path to the running binary.
// Uses os.Executable(); falls back to the default install path.
func detectBinPath() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		// os.Executable can return a path relative to the cwd on some
		// platforms or a path with symlinks; EvalSymlinks resolves to the
		// real binary so rename/replace targets the correct inode.
		if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
			return real
		}
		return exe
	}
	return defaultBinPath
}

// canWriteDir tests whether the process can create and remove a temp file
// in the directory containing `path` (i.e. it can replace the binary).
// On Linux you can rename over a non-writable file if you have write perms
// on the directory, so we test the directory, not the file itself.
// Returns (true, "") on success; (false, reason) on failure so the caller
// can surface the exact OS error (permission denied, read-only fs, etc.)
// instead of the opaque "不可写" message.
func canWriteDir(path string) (bool, string) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gokych-write-test-")
	if err != nil {
		return false, err.Error()
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return true, ""
}

// compareVersions returns true when latest is "newer" than current.
// Both are expected to look like "v0.1.0" or "0.1.0"; we do a simple
// dotted-number comparison. Non-numeric components make us treat the
// versions as not-equal (so "dev" < any real version).
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
					// pre-release / hash suffix — stop parsing; if we
					// haven't seen at least one digit, return nil so the
					// caller knows this isn't a clean semver.
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
		// If we can't parse one side, treat "dev" / hash as older than
		// any real tag; otherwise be conservative and say "not newer".
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
	return false // equal
}

// ── Handlers ──────────────────────────────────────────────────────────

// updateCheckResponse is the JSON body for GET /api/admin/update/check.
type updateCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	Platform        string `json:"platform"`         // "linux/amd64", etc.
	Arch            string `json:"arch"`
	OS              string `json:"os"`
	BinaryPath      string `json:"binary_path"`
	CanWrite        bool   `json:"can_write"`        // binary dir is writable
	CanWriteError   string `json:"can_write_error,omitempty"` // OS error when can_write=false
	ProcessUser     string `json:"process_user,omitempty"`   // os user running the process
	DirPermissions  string `json:"dir_permissions,omitempty"` // octal permissions of bin dir
	PublishedAt     string `json:"published_at,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseNotes    string `json:"release_notes,omitempty"`
	DownloadSize    int64  `json:"download_size,omitempty"`
	Error           string `json:"error,omitempty"`  // non-fatal (e.g. GitHub unreachable)
}

func (s *Server) checkUpdateHandler(c *gin.Context) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	binPath := detectBinPath()

	canW, canWErr := canWriteDir(binPath)

	// Gather permission diagnostics so the admin can see *why* writes fail.
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
		CurrentVersion: s.Version,
		Platform:       goos + "/" + goarch,
		OS:             goos,
		Arch:           goarch,
		BinaryPath:     binPath,
		CanWrite:       canW,
		CanWriteError:  canWErr,
		ProcessUser:    procUser,
		DirPermissions: dirPerms,
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

// applyUpdateRequest is the (empty) body for POST /api/admin/update/apply.
// Future fields (e.g. target_version, force) can go here without breaking.
type applyUpdateRequest struct {
	Version string `json:"version"` // optional: override the tag to install; "" = latest
}

type applyUpdateResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Version   string `json:"version,omitempty"`
	OldBackup string `json:"old_backup,omitempty"`
	Restarting bool  `json:"restarting"`
}

func (s *Server) applyUpdateHandler(c *gin.Context) {
	var req applyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	goos, goarch := runtime.GOOS, runtime.GOARCH
	binPath := detectBinPath()

	if ok, reason := canWriteDir(binPath); !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"error": fmt.Sprintf("cannot write to %s: %s (process running as %s)",
				filepath.Dir(binPath), reason, os.Getenv("USER")),
		})
		return
	}

	client := &http.Client{Timeout: 60 * time.Second}

	// 1. Fetch release metadata.
	var rel *ghRelease
	var err error
	if req.Version != "" {
		// Specific tag: use /releases/tags/:tag endpoint.
		tag := strings.TrimPrefix(req.Version, "v")
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/v%s", githubRepo, tag)
		r, e := fetchReleaseByTag(client, url)
		rel, err = r, e
	} else {
		rel, err = fetchLatestRelease(client)
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// 2. Find platform-matched asset.
	binURL, sumsURL := rel.platformAsset(goos, goarch)
	if binURL == "" {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": fmt.Sprintf("release %s has no asset for %s/%s", rel.TagName, goos, goarch),
		})
		return
	}

	// 3. Download binary to a temp file in the same directory as the
	//    target binary (so rename is atomic — same filesystem).
	dir := filepath.Dir(binPath)
	tmp, err := os.CreateTemp(dir, ".gokych-update-")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create temp file: %v", err)})
		return
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	slog.Info("update: downloading binary", "version", rel.TagName, "url", binURL, "to", tmpPath)
	if err := downloadToFile(client, binURL, tmp); err != nil {
		tmp.Close()
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("download binary: %v", err)})
		return
	}
	tmp.Close()

	// 4. SHA256 verify if SHA256SUMS is present.
	if sumsURL != "" {
		wantHash, hashErr := fetchExpectedHash(client, sumsURL, filepath.Base(binURL))
		if hashErr != nil {
			slog.Warn("update: cannot fetch SHA256SUMS, skipping verification", "err", hashErr)
		} else if wantHash != "" {
			gotHash, herr := fileSHA256(tmpPath)
			if herr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("hash file: %v", herr)})
				return
			}
			if !strings.EqualFold(gotHash, wantHash) {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": fmt.Sprintf("SHA256 mismatch: got %s, want %s — download may be corrupted or tampered", gotHash, wantHash),
				})
				return
			}
			slog.Info("update: SHA256 verified", "hash", gotHash)
		}
	}

	// 5. chmod +x
	if err := os.Chmod(tmpPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("chmod: %v", err)})
		return
	}

	// 6. Back up current binary (best-effort).
	backupPath := binPath + ".prev"
	if err := os.Rename(binPath, backupPath); err != nil {
		// Can't rename (maybe it's a symlink or we don't have perms on
		// the inode). Try copy + remove instead.
		slog.Warn("update: cannot rename current binary for backup, trying copy", "err", err)
		if src, err2 := os.Open(binPath); err2 == nil {
			if dst, err3 := os.Create(backupPath); err3 == nil {
				_, _ = io.Copy(dst, src)
				dst.Close()
				os.Chmod(backupPath, 0755)
			}
			src.Close()
		}
		// If even that fails, proceed — the rename of the new binary
		// over the old one will still work on Unix (the running
		// process keeps the old inode alive).
	} else {
		slog.Info("update: backed up old binary", "backup", backupPath)
	}

	// 7. Atomic rename tmp → binPath.
	if err := os.Rename(tmpPath, binPath); err != nil {
		// Try to restore backup.
		_ = os.Rename(backupPath, binPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("replace binary: %v", err)})
		return
	}
	success = true // don't remove the (now-renamed) tmp in defer

	slog.Info("update: binary replaced successfully",
		"old", backupPath, "new", binPath, "version", rel.TagName)

	// 8. Trigger restart asynchronously (after response is sent).
	restarting := false
	if restartFn != nil {
		restarting = true
		go func() {
			time.Sleep(1 * time.Second) // give HTTP response time to flush
			if err := restartFn(); err != nil {
				slog.Error("update: restart function returned error", "err", err)
			}
		}()
	}

	c.JSON(http.StatusOK, applyUpdateResponse{
		Success:    true,
		Message:    fmt.Sprintf("已更新到 %s，%s", rel.TagName, map[bool]string{true: "服务正在重启...", false: "请手动重启服务"}[restarting]),
		Version:    rel.TagName,
		OldBackup:  backupPath,
		Restarting: restarting,
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

// fetchExpectedHash downloads SHA256SUMS and returns the hex hash for
// the named asset, or "" if the asset isn't listed.
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
		// Format: "<hex>  <filename>" (two spaces between hash and name)
		// or "<hex> *<filename>" (binary-mode marker from sha256sum).
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
