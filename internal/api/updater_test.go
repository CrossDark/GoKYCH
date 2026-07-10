package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeGitCodeDownloadURL(t *testing.T) {
	in := "https://api.gitcode.com/CrossDark/GoKych/releases/download/v0.3.19/gokych-linux-amd64"
	want := "https://gitcode.com/CrossDark/GoKych/releases/download/v0.3.19/gokych-linux-amd64"
	if got := normalizeGitCodeDownloadURL(in); got != want {
		t.Fatalf("normalizeGitCodeDownloadURL() = %q, want %q", got, want)
	}

	alreadyOK := "https://gitcode.com/CrossDark/GoKych/releases/download/v0.3.19/SHA256SUMS"
	if got := normalizeGitCodeDownloadURL(alreadyOK); got != alreadyOK {
		t.Fatalf("normal URL changed: got %q", got)
	}
}

func TestFetchExpectedHashTrimsDotSlashEntries(t *testing.T) {
	const assetName = "gokych-linux-amd64"
	const wantHash = "e69f14818d16240ff614bed3dfeb09a732b2e746318a7f9b18e5e4a144cd3ee7a"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(
			"236e52a90c02248df4177158880b676731ef84102ba83613df1b648feea09045  ./gokych-darwin-amd64\n" +
				wantHash + "  ./" + assetName + "\n"))
	}))
	defer ts.Close()

	got, err := fetchExpectedHash(ts.Client(), ts.URL, assetName)
	if err != nil {
		t.Fatalf("fetchExpectedHash: %v", err)
	}
	if got != wantHash {
		t.Fatalf("hash = %q, want %q", got, wantHash)
	}
}
