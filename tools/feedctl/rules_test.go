package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

const repoRoot = "../.."

func TestRulesAgainstCases(t *testing.T) {
	rules, err := LoadRules(repoRoot + "/rules")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(repoRoot + "/rules/cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, r := range RunCases(rules, cases) {
		if !r.OK {
			t.Errorf("cases.tsv:%d [%s] %q: want %s, got %s (%s)",
				r.Case.Line, r.Case.Profile, r.Case.Entry.Title, r.Case.Expect, r.Got, r.Reason)
		}
	}
}

func TestOPMLKeepReferences(t *testing.T) {
	rules, err := LoadRules(repoRoot + "/rules")
	if err != nil {
		t.Fatal(err)
	}
	feeds, err := LoadOPML(repoRoot + "/feeds/feeds.opml")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkKeepRefs(feeds, rules); err != nil {
		t.Fatal(err)
	}
}

func TestParseRule(t *testing.T) {
	for _, tc := range []struct {
		line    string
		wantErr string
	}{
		{`EntryTitle=(?i)\bC#`, ""},                                       // '#' inside a regex is fine
		{`EntryTitle=(?i)foo=bar`, ""},                                    // split on first '=' only
		{`EntryTitle=(?i)miniflux   # ignore miniflux`, "inline comment"}, // Miniflux docs style: silently broken
		{`EntryTitle=(?i)(unclosed`, "invalid RE2"},
		{`EntryTitle=foo(?=bar)`, "invalid RE2"}, // lookahead is not RE2
		{`Title=foo`, "unknown field"},
		{`EntryTitle=`, "empty pattern"},
		{`no equals sign`, "missing '='"},
		{`EntryDate=max-age:30d`, ""},
		{`EntryDate=between:2026-01-01,2026-12-31`, ""},
		{`EntryDate=last-week`, "invalid EntryDate"},
	} {
		_, err := parseRule(tc.line, 1)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%q: unexpected error %v", tc.line, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%q: want error containing %q, got %v", tc.line, tc.wantErr, err)
		}
	}
}

func TestKeepSemantics(t *testing.T) {
	mustRule := func(s string) Rule {
		r, err := parseRule(s, 1)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	block := []Rule{mustRule(`EntryTitle=(?i)\bC#`)}
	keep := []Rule{mustRule(`EntryTitle=(?i)\bkotlin\b`)}
	e := func(title string) Entry { return Entry{Title: title} }

	if b, _ := IsBlocked(block, nil, nil, keep, e("C# vs Kotlin")); !b {
		t.Error("block must win over keep")
	}
	if b, _ := IsBlocked(nil, nil, nil, keep, e("Rust news")); !b {
		t.Error("non-matching entry must be dropped when keep rules exist")
	}
	if b, _ := IsBlocked(nil, nil, nil, nil, e("Rust news")); b {
		t.Error("no rules must keep everything")
	}
	// A user (global) keep rule restricts every feed, even feeds without keep rules.
	if b, _ := IsBlocked(nil, nil, keep, nil, e("Rust news")); !b {
		t.Error("global keep must restrict all feeds")
	}
}

func TestDateRules(t *testing.T) {
	old := Entry{Date: time.Now().Add(-40 * 24 * time.Hour)}
	recent := Entry{Date: time.Now().Add(-2 * 24 * time.Hour)}
	r, _ := parseRule(`EntryDate=max-age:30d`, 1)
	if !r.Matches(old) || r.Matches(recent) {
		t.Error("max-age:30d must match entries older than 30 days only (Miniflux semantics)")
	}
}

func TestBuildOPMLRoundTrip(t *testing.T) {
	in := []OPMLFeed{
		{Title: `A & "B"`, URL: "https://hnrss.org/newest?q=Java+OR+Kotlin&points=20", Category: "Firehose"},
		{Title: "C", URL: "https://example.org/feed.xml", Category: "Tech"},
	}
	path := t.TempDir() + "/x.opml"
	if err := os.WriteFile(path, BuildOPML(in), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := LoadOPML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].URL != in[0].URL || out[0].Title != in[0].Title || out[0].Category != "Firehose" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}
