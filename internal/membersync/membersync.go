// Package membersync builds the membership cache (and an inventory for
// validation) from the GitLab REST API. It runs OFF the push path, on the
// Jenkins agent: the API token lives in Jenkins credentials only and never
// reaches the GitLab server's disk.
//
// Semantics follow GitLab: a user belongs to group G when they are a member
// of G directly or through an ancestor group (/groups/:id/members/all).
// Membership of a SUBgroup does not make someone a member of the parent.
package membersync

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/version"
)

// Client is a minimal GitLab API v4 client.
type Client struct {
	BaseURL string // e.g. https://gitlab.example.com (no /api/v4)
	Token   string
	HTTP    *http.Client
	// MaxRetries for 429/5xx/network errors.
	MaxRetries int
	sleep      func(time.Duration)
}

// NewClient builds a client. caFile adds a CA bundle; insecure disables TLS
// verification (lab only).
func NewClient(baseURL, token, caFile string, insecure bool, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid GitLab URL %q", baseURL)
	}
	if token == "" {
		return nil, errors.New("no GitLab token (set GITLAB_TOKEN or --token-file)")
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f {
			return nil, errors.New("GitLab token contains whitespace or control characters (check the credential)")
		}
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec // explicit lab-only flag
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s: no certificates found", caFile)
		}
		tlsCfg.RootCAs = pool
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return &Client{BaseURL: u.String(), Token: token, MaxRetries: 4, sleep: time.Sleep,
		HTTP: &http.Client{Timeout: timeout, Transport: tr}}, nil
}

// ErrNotFound is returned for HTTP 404.
var ErrNotFound = errors.New("not found")

// get performs one GET with retries and returns body and next page ("" = last).
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, string, error) {
	u := c.BaseURL + "/api/v4" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("PRIVATE-TOKEN", c.Token)
		req.Header.Set("User-Agent", "git-policy/"+version.Version)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			resp.Body.Close()
			switch {
			case rerr != nil:
				lastErr = rerr
			case resp.StatusCode == http.StatusOK:
				return body, resp.Header.Get("X-Next-Page"), nil
			case resp.StatusCode == http.StatusNotFound:
				return nil, "", ErrNotFound
			case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
				// Never echo the token; the body is GitLab's short error JSON.
				return nil, "", fmt.Errorf("GitLab API %s: HTTP %d (token invalid or lacking read_api/admin)", path, resp.StatusCode)
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				lastErr = fmt.Errorf("GitLab API %s: HTTP %d", path, resp.StatusCode)
				if s, _ := strconv.Atoi(resp.Header.Get("Retry-After")); s > 0 && s <= 60 {
					c.sleep(time.Duration(s) * time.Second)
					continue
				}
			default:
				return nil, "", fmt.Errorf("GitLab API %s: HTTP %d", path, resp.StatusCode)
			}
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		c.sleep(time.Duration(1<<attempt) * 500 * time.Millisecond)
	}
	return nil, "", fmt.Errorf("giving up after %d attempts: %w", c.MaxRetries+1, lastErr)
}

// getAll follows X-Next-Page pagination and decodes each page into T.
func getAll[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("per_page", "100")
	page := "1"
	var out []T
	for page != "" {
		q.Set("page", page)
		body, next, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("GitLab API %s: %w", path, err)
		}
		out = append(out, items...)
		page = next
	}
	return out, nil
}

type apiGroup struct {
	ID       int    `json:"id"`
	FullPath string `json:"full_path"`
}

type apiMember struct {
	Username string `json:"username"`
	State    string `json:"state"`
}

type apiUser struct {
	Username string `json:"username"`
	State    string `json:"state"`
}

type apiProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

// Inventory lists what exists in GitLab, for validator warnings (W010).
type Inventory struct {
	Schema      string    `json:"schema"`
	GeneratedAt time.Time `json:"generated_at"`
	Users       []string  `json:"users"`
	Groups      []string  `json:"groups"`
	Projects    []string  `json:"projects"`
}

// InventorySchema identifies inventory files.
const InventorySchema = "git-policy/inventory/v1"

// Membership fetches the members (incl. inherited) of each group path.
// Every requested group must exist: a partial result is never produced.
func (c *Client) Membership(ctx context.Context, groups []string, now time.Time) (*membership.File, error) {
	f := &membership.File{
		Schema: membership.Schema, GeneratedAt: now.UTC(),
		Generator: "git-policy " + version.Version + " sync-membership",
		Source:    map[string]string{"gitlab_url_sha256": shaHex(c.BaseURL)},
		Users:     map[string]membership.UserEntry{},
	}
	seen := map[string]bool{}
	for _, g := range groups {
		g = strings.ToLower(strings.Trim(g, "/ "))
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		body, _, err := c.get(ctx, "/groups/"+url.PathEscape(g), nil)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("group %q referenced by the policy does not exist in GitLab (fix the policy; nothing was written)", g)
		}
		if err != nil {
			return nil, err
		}
		var grp apiGroup
		if err := json.Unmarshal(body, &grp); err != nil || grp.ID == 0 {
			return nil, fmt.Errorf("group %q: unexpected API response", g)
		}
		members, err := getAll[apiMember](ctx, c, "/groups/"+strconv.Itoa(grp.ID)+"/members/all", nil)
		if err != nil {
			return nil, err
		}
		f.Groups = append(f.Groups, g)
		for _, m := range members {
			u := strings.ToLower(m.Username)
			e := f.Users[u]
			e.State = m.State
			if !contains(e.Groups, g) {
				e.Groups = append(e.Groups, g)
			}
			f.Users[u] = e
		}
	}
	sort.Strings(f.Groups)
	for u, e := range f.Users {
		sort.Strings(e.Groups)
		f.Users[u] = e
	}
	return f, nil
}

// FetchInventory lists all users, groups and projects (admin token needed
// for a complete user list).
func (c *Client) FetchInventory(ctx context.Context, now time.Time) (*Inventory, error) {
	users, err := getAll[apiUser](ctx, c, "/users", nil)
	if err != nil {
		return nil, err
	}
	groups, err := getAll[apiGroup](ctx, c, "/groups", url.Values{"all_available": {"true"}})
	if err != nil {
		return nil, err
	}
	projects, err := getAll[apiProject](ctx, c, "/projects", url.Values{"simple": {"true"}})
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Schema: InventorySchema, GeneratedAt: now.UTC()}
	for _, u := range users {
		inv.Users = append(inv.Users, strings.ToLower(u.Username))
	}
	for _, g := range groups {
		inv.Groups = append(inv.Groups, strings.ToLower(g.FullPath))
	}
	for _, p := range projects {
		inv.Projects = append(inv.Projects, strings.ToLower(p.PathWithNamespace))
	}
	sort.Strings(inv.Users)
	sort.Strings(inv.Groups)
	sort.Strings(inv.Projects)
	return inv, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func shaHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
