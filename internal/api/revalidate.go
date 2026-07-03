package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// revalidateReq is the body POSTed to the frontend on-demand revalidation
// endpoint (see web/app/revalidate/route.ts). `tags` maps to Next.js
// revalidateTag() and `paths` to revalidatePath(). Either may be empty.
type revalidateReq struct {
	Tags  []string `json:"tags,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

var revalidateHTTPClient = &http.Client{Timeout: 6 * time.Second}

// revalidateFrontend fire-and-forget POSTs a revalidate request to every
// configured frontend (Cloudflare Workers + EdgeOne, comma-separated in
// FRONTEND_REVALIDATE_URLS). It is deliberately best-effort: a failed
// webhook must never block or fail a user mutation — the ISR revalidate
// window (and the origin Cache-Control SWR) is the safety net. Mutations
// call this after the DB write succeeds so the freshly cached HTML/data
// across both frontends refreshes within seconds instead of waiting up
// to the revalidate window (article detail = 30 min otherwise).
func (s *Server) revalidateFrontend(tags []string, paths []string) {
	urls := os.Getenv("FRONTEND_REVALIDATE_URLS")
	secret := os.Getenv("REVALIDATE_SECRET")
	if urls == "" || secret == "" {
		return // not configured — silently rely on time-based revalidation
	}
	body, err := json.Marshal(revalidateReq{Tags: tags, Paths: paths})
	if err != nil {
		return
	}
	for _, base := range strings.Split(urls, ",") {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		go func(target string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+secret)
			resp, err := revalidateHTTPClient.Do(req)
			if err != nil {
				slog.Debug("revalidate: request failed", "url", target, "err", err)
				return
			}
			resp.Body.Close()
		}(base)
	}
}

// revalidateArticleTags builds the cache-tag set for a mutation affecting a
// single article's content/comments/rating: the global "articles" list tag,
// the "home" tag (featured/recent), the per-article tag, and optionally the
// "labels" tag (when tags on the article changed, label counts shift).
func revalidateArticleTags(atype, slug string, tagsChanged bool) []string {
	tags := []string{"articles", "home", "article:" + atype + ":" + slug}
	if tagsChanged {
		tags = append(tags, "labels")
	}
	return tags
}

// revalidateArticlePaths builds the path set to revalidatePath for a single
// article mutation: the article's own page, its type list, the home page,
// and (if tags changed) the label cloud + label-list pages.
func revalidateArticlePaths(atype, slug string, tagsChanged bool) []string {
	paths := []string{
		"/" + atype + "/" + slug,
		"/" + atype,
		"/",
	}
	if tagsChanged {
		paths = append(paths, "/labels")
	}
	return paths
}
