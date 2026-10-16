package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file mirrors the entry-filter semantics of Miniflux
// (internal/reader/filter/filter.go, verified against v2.3.3 and main @ c317f84):
//
//   - rule format: Field=Regex, one per line, split on the first '='; both sides trimmed
//   - RE2 syntax (Go regexp), invalid rules are silently ignored by Miniflux
//   - order: user block, feed block, user keep, feed keep; first match wins
//   - user and feed rules are merged into one block list and one keep list
//   - if the merged keep list is non-empty, entries matching none of it are dropped
//   - EntryTag is matched against each tag separately
//
// Legacy per-feed `blocklist_rules` / `keeplist_rules` (single regex) are not modelled.

var validFields = map[string]bool{
	"EntryTitle":       true,
	"EntryURL":         true,
	"EntryCommentsURL": true,
	"EntryContent":     true,
	"EntryAuthor":      true,
	"EntryTag":         true,
	"EntryDate":        true,
}

// inlineComment flags "regex   # comment". Miniflux has no comment syntax, so the
// trailing text becomes part of the regex and the rule silently never matches.
// "\bC#" is not flagged: only whitespace-'#'-(whitespace|end) is.
var inlineComment = regexp.MustCompile(`\s#(\s|$)`)

type Rule struct {
	Field   string
	Pattern string
	Line    int
	re      *regexp.Regexp
}

func (r Rule) String() string { return r.Field + "=" + r.Pattern }

type RuleSet struct {
	Name  string
	Path  string
	Rules []Rule
}

// Serialize returns the exact text sent to Miniflux: rules only, no comments.
func (rs RuleSet) Serialize() string {
	lines := make([]string, len(rs.Rules))
	for i, r := range rs.Rules {
		lines[i] = r.String()
	}
	return strings.Join(lines, "\n")
}

func LoadRuleSet(path string) (RuleSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return RuleSet{}, err
	}
	defer f.Close()

	rs := RuleSet{Name: strings.TrimSuffix(filepath.Base(path), ".rules"), Path: path}
	var errs []string
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := parseRule(line, n)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s:%d: %v", path, n, err))
			continue
		}
		rs.Rules = append(rs.Rules, r)
	}
	if err := sc.Err(); err != nil {
		return RuleSet{}, err
	}
	if len(errs) > 0 {
		return RuleSet{}, errors.New(strings.Join(errs, "\n"))
	}
	return rs, nil
}

func parseRule(line string, n int) (Rule, error) {
	field, pattern, ok := strings.Cut(line, "=")
	if !ok {
		return Rule{}, fmt.Errorf("missing '=' in %q", line)
	}
	field, pattern = strings.TrimSpace(field), strings.TrimSpace(pattern)
	if !validFields[field] {
		return Rule{}, fmt.Errorf("unknown field %q (Miniflux would never match it)", field)
	}
	if pattern == "" {
		return Rule{}, errors.New("empty pattern")
	}
	if inlineComment.MatchString(pattern) {
		return Rule{}, errors.New("looks like an inline comment; Miniflux would treat it as part of the regex. Put comments on their own line")
	}
	r := Rule{Field: field, Pattern: pattern, Line: n}
	if field == "EntryDate" {
		if !validDatePattern(pattern) {
			return Rule{}, fmt.Errorf("invalid EntryDate pattern %q (future | before:YYYY-MM-DD | after:YYYY-MM-DD | between:YYYY-MM-DD,YYYY-MM-DD | max-age:<n>d|<go duration>)", pattern)
		}
		return r, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Rule{}, fmt.Errorf("invalid RE2 regex (Miniflux would silently ignore this rule): %v", err)
	}
	r.re = re
	return r, nil
}

type Entry struct {
	Title       string
	URL         string
	CommentsURL string
	Content     string
	Author      string
	Tags        []string
	Date        time.Time
}

func (r Rule) Matches(e Entry) bool {
	switch r.Field {
	case "EntryDate":
		return matchDate(r.Pattern, e.Date)
	case "EntryTitle":
		return r.re.MatchString(e.Title)
	case "EntryURL":
		return r.re.MatchString(e.URL)
	case "EntryCommentsURL":
		return r.re.MatchString(e.CommentsURL)
	case "EntryContent":
		return r.re.MatchString(e.Content)
	case "EntryAuthor":
		return r.re.MatchString(e.Author)
	case "EntryTag":
		for _, t := range e.Tags {
			if r.re.MatchString(t) {
				return true
			}
		}
	}
	return false
}

func firstMatch(rules []Rule, e Entry) (Rule, bool) {
	for _, r := range rules {
		if r.Matches(e) {
			return r, true
		}
	}
	return Rule{}, false
}

// IsBlocked mirrors miniflux filter.IsBlockedEntry for filter-entry rules.
func IsBlocked(userBlock, feedBlock, userKeep, feedKeep []Rule, e Entry) (bool, string) {
	block := append(append([]Rule{}, userBlock...), feedBlock...)
	if r, ok := firstMatch(block, e); ok {
		return true, "block rule " + r.String()
	}
	keep := append(append([]Rule{}, userKeep...), feedKeep...)
	if len(keep) > 0 {
		if r, ok := firstMatch(keep, e); ok {
			return false, "keep rule " + r.String()
		}
		return true, "no keep rule matched"
	}
	return false, "no rule matched"
}

func validDatePattern(p string) bool {
	if p == "future" {
		return true
	}
	kind, val, ok := strings.Cut(p, ":")
	if !ok {
		return false
	}
	switch kind {
	case "before", "after":
		_, err := time.Parse("2006-01-02", val)
		return err == nil
	case "between":
		a, b, ok := strings.Cut(val, ",")
		if !ok {
			return false
		}
		_, errA := time.Parse("2006-01-02", a)
		_, errB := time.Parse("2006-01-02", b)
		return errA == nil && errB == nil
	case "max-age":
		_, err := parseDuration(val)
		return err == nil
	}
	return false
}

// matchDate mirrors Miniflux. Note: max-age matches entries OLDER than the duration.
func matchDate(p string, d time.Time) bool {
	if p == "future" {
		return d.After(time.Now())
	}
	kind, val, _ := strings.Cut(p, ":")
	switch kind {
	case "before":
		t, err := time.Parse("2006-01-02", val)
		return err == nil && d.Before(t)
	case "after":
		t, err := time.Parse("2006-01-02", val)
		return err == nil && d.After(t)
	case "between":
		a, b, _ := strings.Cut(val, ",")
		ta, errA := time.Parse("2006-01-02", a)
		tb, errB := time.Parse("2006-01-02", b)
		return errA == nil && errB == nil && d.After(ta) && d.Before(tb)
	case "max-age":
		dur, err := parseDuration(val)
		return err == nil && d.Before(time.Now().Add(-dur))
	}
	return false
}

func parseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n := 0
		if days != "" {
			var err error
			if n, err = strconv.Atoi(days); err != nil {
				return 0, err
			}
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// Rules is the repository's rule configuration.
type Rules struct {
	GlobalBlock RuleSet            // -> user block_filter_entry_rules
	FeedKeep    map[string]RuleSet // keep-*.rules -> per-feed keep_filter_entry_rules
}

func LoadRules(dir string) (Rules, error) {
	if _, err := os.Stat(filepath.Join(dir, "global-keep.rules")); err == nil {
		return Rules{}, errors.New("rules/global-keep.rules exists: global keep rules are forbidden (Miniflux merges them into every feed's keep list)")
	}
	gb, err := LoadRuleSet(filepath.Join(dir, "global-block.rules"))
	if err != nil {
		return Rules{}, err
	}
	r := Rules{GlobalBlock: gb, FeedKeep: map[string]RuleSet{}}
	paths, err := filepath.Glob(filepath.Join(dir, "keep-*.rules"))
	if err != nil {
		return Rules{}, err
	}
	sort.Strings(paths)
	var errs []string
	for _, p := range paths {
		rs, err := LoadRuleSet(p)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if len(rs.Rules) == 0 {
			errs = append(errs, p+": no rules (an empty keep set would be a no-op)")
			continue
		}
		r.FeedKeep[rs.Name] = rs
	}
	if len(errs) > 0 {
		return Rules{}, errors.New(strings.Join(errs, "\n"))
	}
	return r, nil
}

type Case struct {
	Line    int
	Expect  string // keep | block
	Profile string // none | keep-*.rules name
	Entry   Entry
	Note    string
}

func LoadCases(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cases []Case
	var errs []string
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "expect\t") {
			continue
		}
		cols := strings.Split(raw, "\t")
		for len(cols) < 6 {
			cols = append(cols, "")
		}
		c := Case{Line: n, Expect: strings.TrimSpace(cols[0]), Profile: strings.TrimSpace(cols[1]), Note: strings.TrimSpace(cols[5])}
		c.Entry.Title = strings.TrimSpace(cols[2])
		for _, t := range strings.Split(cols[3], ",") {
			if t = strings.TrimSpace(t); t != "" {
				c.Entry.Tags = append(c.Entry.Tags, t)
			}
		}
		c.Entry.URL = strings.TrimSpace(cols[4])
		if c.Expect != "keep" && c.Expect != "block" {
			errs = append(errs, fmt.Sprintf("%s:%d: expect must be keep|block, got %q", path, n, c.Expect))
			continue
		}
		if c.Profile == "" || c.Entry.Title == "" {
			errs = append(errs, fmt.Sprintf("%s:%d: profile and title are required", path, n))
			continue
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "\n"))
	}
	return cases, nil
}

type CaseResult struct {
	Case   Case
	Got    string
	Reason string
	OK     bool
}

// RunCases evaluates every case the way Miniflux would: global block rules as the
// user block list, no user keep rules, and the profile's keep set as feed keep rules.
func RunCases(r Rules, cases []Case) []CaseResult {
	out := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		var feedKeep []Rule
		if c.Profile != "none" {
			rs, ok := r.FeedKeep[c.Profile]
			if !ok {
				out = append(out, CaseResult{Case: c, Got: "error", Reason: "unknown profile " + c.Profile})
				continue
			}
			feedKeep = rs.Rules
		}
		blocked, why := IsBlocked(r.GlobalBlock.Rules, nil, nil, feedKeep, c.Entry)
		got := "keep"
		if blocked {
			got = "block"
		}
		out = append(out, CaseResult{Case: c, Got: got, Reason: why, OK: got == c.Expect})
	}
	return out
}
