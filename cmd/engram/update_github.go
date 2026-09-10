//go:build !noupdate

package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/poorants/engram/pkg/selfupdate"
)

// updateRepo is where this binary's own releases live — the same repo
// install.sh downloads from. Kept as a constant rather than a flag: a binary
// that checked a DIFFERENT project for its own updates would be a footgun, not
// a feature.
const updateRepo = "poorants/engram"

var tagFromURL = regexp.MustCompile(`/releases/tag/([^/]+)/?$`)

// latestReleaseTag follows the /releases/latest redirect and reads the tag out
// of where it lands.
//
// That is deliberate, and it is the same choice install.sh makes: the obvious
// alternative, the GitHub API, is rate-limited per IP for unauthenticated
// callers — 60 requests an hour shared by everyone behind one office NAT. A
// version check that starts failing once a few people are working is worse than
// no check, because the silence is indistinguishable from "you are current".
func latestReleaseTag(ctx context.Context) (string, error) {
	url := "https://github.com/" + updateRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// resp.Request is the LAST request made, so its URL is where the redirect
	// chain landed: .../releases/tag/v0.4.0
	m := tagFromURL.FindStringSubmatch(resp.Request.URL.Path)
	if m == nil {
		return "", fmt.Errorf("no release tag in %s", resp.Request.URL)
	}
	return m[1], nil
}

// newUpdateChecker wires the cache-backed checker to GitHub. Unlike the store,
// this needs no credential — the releases are public — so the check works on a
// machine that has not been configured yet.
func newUpdateChecker() selfupdate.Checker {
	return selfupdate.Checker{
		Current: resolveVersion(),
		Path:    selfupdate.CachePath(),
		Fetch:   latestReleaseTag,
	}
}
