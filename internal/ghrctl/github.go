package ghrctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Runner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

type github struct {
	base   string
	client *http.Client
}

func newGitHub() github {
	return github{"https://api.github.com", &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type apiError struct{ Status int }

func (e apiError) Error() string {
	return fmt.Sprintf("GitHub HTTP %d; check organization, PAT permissions/expiry, organization approval and network access", e.Status)
}

func (g github) request(ctx context.Context, method, org, path, token string, out any) error {
	u := g.base + "/orgs/" + url.PathEscape(org) + "/actions/runners" + path
	r, e := http.NewRequestWithContext(ctx, method, u, nil)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	r.Header.Set("User-Agent", "ghrctl")
	resp, e := g.client.Do(r)
	if e != nil {
		return fmt.Errorf("GitHub request failed: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError{resp.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	return nil
}

func (g github) runner(ctx context.Context, c Config, token string, id int64) (Runner, error) {
	var r Runner
	e := g.request(ctx, "GET", c.Organization, "/"+strconv.FormatInt(id, 10), token, &r)
	return r, e
}

func (g github) validate(ctx context.Context, c Config, token string, ownID int64) error {
	for page := 1; ; page++ {
		var result struct {
			Runners []Runner `json:"runners"`
		}
		path := "?per_page=100&page=" + strconv.Itoa(page)
		if e := g.request(ctx, "GET", c.Organization, path, token, &result); e != nil {
			return e
		}
		for _, r := range result.Runners {
			if r.Name == c.Name && r.ID != ownID {
				return fmt.Errorf("runner name %q already exists in GitHub; choose another name or remove a confirmed stale runner", c.Name)
			}
		}
		if len(result.Runners) < 100 {
			break
		}
	}
	var result struct {
		Token string `json:"token"`
	}
	if e := g.request(ctx, "POST", c.Organization, "/registration-token", token, &result); e != nil {
		return e
	}
	if result.Token == "" {
		return fmt.Errorf("GitHub returned an empty registration token")
	}
	return nil
}
