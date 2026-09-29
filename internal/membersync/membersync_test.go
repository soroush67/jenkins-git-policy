package membersync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitLab serves the few endpoints sync needs, with pagination.
func fakeGitLab(t *testing.T, token string) (*httptest.Server, *int32) {
	t.Helper()
	var throttled int32
	groups := map[string]int{"contractors": 11, "finance/qa": 12}
	members := map[int][]map[string]string{
		11: {{"username": "Carol", "state": "active"}, {"username": "dave", "state": "active"}, {"username": "eve", "state": "blocked"}},
		12: {{"username": "carol", "state": "active"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != token {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		p := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
		switch {
		case strings.HasPrefix(p, "/groups/") && strings.HasSuffix(p, "/members/all"):
			var id int
			fmt.Sscanf(strings.TrimPrefix(p, "/groups/"), "%d", &id)
			if id == 11 && atomic.AddInt32(&throttled, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			all := members[id]
			page := r.URL.Query().Get("page")
			// one member per page to exercise pagination
			i := 0
			fmt.Sscanf(page, "%d", &i)
			if i < 1 || i > len(all) {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			if i < len(all) {
				w.Header().Set("X-Next-Page", fmt.Sprint(i+1))
			}
			json.NewEncoder(w).Encode(all[i-1 : i])
		case strings.HasPrefix(p, "/groups/"):
			path := strings.ReplaceAll(strings.TrimPrefix(p, "/groups/"), "%2F", "/")
			id, ok := groups[path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "full_path": path})
		case p == "/groups":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 11, "full_path": "contractors"}, {"id": 12, "full_path": "finance/qa"}})
		case p == "/users":
			json.NewEncoder(w).Encode([]map[string]string{{"username": "carol"}, {"username": "Dave"}})
		case p == "/projects":
			json.NewEncoder(w).Encode([]map[string]string{{"path_with_namespace": "finance/app"}})
		default:
			w.WriteHeader(404)
		}
	})
	return httptest.NewServer(mux), &throttled
}

func client(t *testing.T, url, token string) *Client {
	c, err := NewClient(url, token, "", false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(time.Duration) {} // no real waiting in tests
	return c
}

func TestMembershipSync(t *testing.T) {
	srv, throttled := fakeGitLab(t, "secret-token")
	defer srv.Close()
	c := client(t, srv.URL, "secret-token")
	f, err := c.Membership(context.Background(), []string{"contractors", "Finance/QA", "contractors"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if *throttled < 2 {
		t.Error("429 was not retried")
	}
	if strings.Join(f.Groups, ",") != "contractors,finance/qa" {
		t.Errorf("groups %v", f.Groups)
	}
	if g := f.Users["carol"].Groups; strings.Join(g, ",") != "contractors,finance/qa" {
		t.Errorf("carol (case-folded, 2 groups across pages): %v", g)
	}
	if f.Users["eve"].State != "blocked" || f.Pairs() != 4 {
		t.Errorf("eve %+v pairs %d", f.Users["eve"], f.Pairs())
	}
	if f.Source["gitlab_url_sha256"] == "" || strings.Contains(fmt.Sprint(f.Source), srv.URL) {
		t.Error("source must be a hash, not the URL")
	}
}

func TestMembershipSyncFailsClosed(t *testing.T) {
	srv, _ := fakeGitLab(t, "secret-token")
	defer srv.Close()
	if _, err := client(t, srv.URL, "secret-token").Membership(context.Background(), []string{"contractors", "no-such-group"}, time.Now()); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing group must fail the whole sync: %v", err)
	}
	_, err := client(t, srv.URL, "wrong-token").Membership(context.Background(), []string{"contractors"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("401 must fail without echoing the token: %v", err)
	}
	down := client(t, "http://127.0.0.1:1", "t")
	down.MaxRetries = 1
	if _, err := down.Membership(context.Background(), []string{"x"}, time.Now()); err == nil {
		t.Fatal("unreachable GitLab must fail")
	}
}

func TestInventory(t *testing.T) {
	srv, _ := fakeGitLab(t, "secret-token")
	defer srv.Close()
	inv, err := client(t, srv.URL, "secret-token").FetchInventory(context.Background(), time.Now())
	if err != nil || strings.Join(inv.Users, ",") != "carol,dave" || len(inv.Groups) != 2 || inv.Projects[0] != "finance/app" {
		t.Fatalf("%+v %v", inv, err)
	}
}

func TestNewClientValidation(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "gitlab.local", "http://"} {
		if _, err := NewClient(u, "t", "", false, time.Second); err == nil {
			t.Errorf("accepted URL %q", u)
		}
	}
	if _, err := NewClient("https://gitlab.local", "", "", false, time.Second); err == nil {
		t.Error("empty token accepted")
	}
	if _, err := NewClient("https://gitlab.local", "glpat-abc\n", "", false, time.Second); err == nil {
		t.Error("token with newline accepted")
	}
}
