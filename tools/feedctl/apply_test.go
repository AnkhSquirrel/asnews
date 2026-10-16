package main

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeMiniflux implements the slice of the Miniflux v2.3.x API that feedctl uses.
// It does not model Miniflux internals (URL normalisation, fetching); see CLAUDE.md.
type fakeMiniflux struct {
	mu     sync.Mutex
	user   User
	feeds  []Feed
	writes []string
	token  string
	// Basic auth credentials; accepted alongside the token when set.
	username, password string
}

func (f *fakeMiniflux) authorized(r *http.Request) bool {
	if f.token != "" && r.Header.Get("X-Auth-Token") == f.token {
		return true
	}
	u, p, ok := r.BasicAuth()
	return ok && f.username != "" && u == f.username && p == f.password
}

func (f *fakeMiniflux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.authorized(r) {
		http.Error(w, `{"error_message":"access unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/me":
		_ = json.NewEncoder(w).Encode(f.user)
	case r.Method == "PUT" && r.URL.Path == "/v1/users/"+strconv.FormatInt(f.user.ID, 10):
		var m map[string]*string
		_ = json.NewDecoder(r.Body).Decode(&m)
		if v := m["block_filter_entry_rules"]; v != nil {
			f.user.BlockFilterEntryRules = *v
		}
		if v := m["keep_filter_entry_rules"]; v != nil {
			f.user.KeepFilterEntryRules = *v
		}
		f.writes = append(f.writes, "PUT user")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(f.user)
	case r.Method == "GET" && r.URL.Path == "/v1/feeds":
		_ = json.NewEncoder(w).Encode(f.feeds)
	case r.Method == "POST" && r.URL.Path == "/v1/import":
		body, _ := io.ReadAll(r.Body)
		var doc opmlDoc
		if err := xml.Unmarshal(body, &doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, cat := range doc.Body.Outlines {
			for _, o := range cat.Children {
				fd := Feed{ID: int64(len(f.feeds) + 1), Title: o.Text, FeedURL: o.XMLURL}
				fd.Category.Title = cat.Text
				f.feeds = append(f.feeds, fd)
			}
		}
		f.writes = append(f.writes, "POST import")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"message":"Feeds imported successfully"}`))
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/feeds/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/feeds/"), 10, 64)
		var m map[string]*string
		_ = json.NewDecoder(r.Body).Decode(&m)
		for i := range f.feeds {
			if f.feeds[i].ID == id {
				if v := m["keep_filter_entry_rules"]; v != nil {
					f.feeds[i].KeepFilterEntryRules = *v
				}
				f.writes = append(f.writes, "PUT feed "+f.feeds[i].FeedURL)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(f.feeds[i])
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func TestApplyIdempotent(t *testing.T) {
	rules, err := LoadRules(repoRoot + "/rules")
	if err != nil {
		t.Fatal(err)
	}
	feeds, err := LoadOPML(repoRoot + "/feeds/feeds.opml")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeMiniflux{
		token: "secret",
		user:  User{ID: 1, Username: "admin", KeepFilterEntryRules: "EntryTitle=(?i)java"}, // stray global keep
		// one feed already present, with a stale keep rule that must be cleared
		feeds: []Feed{{ID: 1, Title: "LWN", FeedURL: "https://lwn.net/headlines/rss", KeepFilterEntryRules: "EntryTitle=x"}},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c := NewClient(srv.URL, "secret")

	// Dry run changes nothing.
	var out strings.Builder
	if err := runApply(c, rules, feeds, true, &out); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("dry-run wrote: %v", fake.writes)
	}

	// First apply.
	out.Reset()
	if err := runApply(c, rules, feeds, false, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if fake.user.BlockFilterEntryRules != rules.GlobalBlock.Serialize() {
		t.Error("global block rules not set")
	}
	for _, l := range strings.Split(fake.user.BlockFilterEntryRules, "\n") {
		if strings.HasPrefix(l, "#") || strings.TrimSpace(l) == "" {
			t.Errorf("comment or blank line uploaded: %q", l)
		}
	}
	if fake.user.KeepFilterEntryRules != "" {
		t.Error("global keep rules not cleared")
	}
	if len(fake.feeds) != len(feeds) {
		t.Fatalf("want %d feeds after import, got %d", len(feeds), len(fake.feeds))
	}
	byURL := map[string]Feed{}
	for _, f := range fake.feeds {
		byURL[f.FeedURL] = f
	}
	for _, f := range feeds {
		got := byURL[f.URL].KeepFilterEntryRules
		want := ""
		if f.Keep != "" {
			want = rules.FeedKeep[f.Keep].Serialize()
		}
		if got != want {
			t.Errorf("%s: keep rules = %q, want %q", f.URL, got, want)
		}
	}
	if byURL["https://lwn.net/headlines/rss"].Category.Title != "" {
		t.Error("pre-existing feed must not be re-imported")
	}

	// Second apply: no writes.
	fake.writes = nil
	out.Reset()
	if err := runApply(c, rules, feeds, false, &out); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("second apply not idempotent, writes: %v\n%s", fake.writes, out.String())
	}
}

func TestApplyBadToken(t *testing.T) {
	srv := httptest.NewServer(&fakeMiniflux{token: "secret"})
	defer srv.Close()
	err := runApply(NewClient(srv.URL, "wrong"), Rules{}, nil, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error, got %v", err)
	}
}

func TestApplyBasicAuth(t *testing.T) {
	fake := &fakeMiniflux{username: "admin", password: "pw", user: User{ID: 1, Username: "admin"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := runApply(NewBasicClient(srv.URL, "admin", "pw"), Rules{}, nil, true, io.Discard); err != nil {
		t.Fatalf("basic auth rejected: %v", err)
	}
	err := runApply(NewBasicClient(srv.URL, "admin", "wrong"), Rules{}, nil, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error, got %v", err)
	}
}
