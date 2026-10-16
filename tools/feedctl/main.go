// Command feedctl validates the repo's Miniflux filter rules and feed list, and applies
// them to a Miniflux instance through its REST API. The repo is the source of truth.
//
//	go run ./tools/feedctl check  [-rules rules] [-v]
//	go run ./tools/feedctl apply  [-url URL] [-rules rules] [-opml feeds/feeds.opml] [-dry-run]
//	go run ./tools/feedctl probe  [-opml feeds/feeds.opml]
//
// apply reads MINIFLUX_URL from the environment: http://miniflux:8080 inside the compose stack,
// the nginx URL from elsewhere (Miniflux publishes no host port). There is deliberately no
// localhost default. It authenticates with MINIFLUX_TOKEN (Settings > API Keys) if set,
// otherwise with MINIFLUX_USERNAME and MINIFLUX_PASSWORD (HTTP basic auth).
package main

import (
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = cmdCheck(os.Args[2:])
	case "apply":
		err = cmdApply(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "feedctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  feedctl check  [-rules DIR] [-v]                      validate rules, run rules/cases.tsv
  feedctl apply  [-url URL] [-rules DIR] [-opml FILE] [-dry-run]
                                                        sync rules + feeds to Miniflux (needs MINIFLUX_URL and
                                                        MINIFLUX_TOKEN, or MINIFLUX_USERNAME + MINIFLUX_PASSWORD)
  feedctl probe  [-opml FILE]                           fetch every feed URL, report HTTP status + feed type`)
}

// loadAndTest loads rules + cases and fails on any regression.
func loadAndTest(rulesDir string, verbose bool) (Rules, error) {
	rules, err := LoadRules(rulesDir)
	if err != nil {
		return Rules{}, err
	}
	cases, err := LoadCases(rulesDir + "/cases.tsv")
	if err != nil {
		return Rules{}, err
	}
	results := RunCases(rules, cases)
	failed := 0
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, r := range results {
		if !r.OK {
			failed++
		}
		if verbose || !r.OK {
			status := "ok"
			if !r.OK {
				status = "FAIL"
			}
			fmt.Fprintf(tw, "%s\tline %d\t%s\twant %s\tgot %s\t%q\t%s\n",
				status, r.Case.Line, r.Case.Profile, r.Case.Expect, r.Got, r.Case.Entry.Title, r.Reason)
		}
	}
	tw.Flush()
	nKeep := 0
	for _, rs := range rules.FeedKeep {
		nKeep += len(rs.Rules)
	}
	fmt.Printf("rules: %d global block, %d keep (%d sets); cases: %d, failed: %d\n",
		len(rules.GlobalBlock.Rules), nKeep, len(rules.FeedKeep), len(results), failed)
	if failed > 0 {
		return Rules{}, fmt.Errorf("%d case(s) failed", failed)
	}
	return rules, nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	rulesDir := fs.String("rules", "rules", "rules directory")
	opmlPath := fs.String("opml", "feeds/feeds.opml", "OPML feed list (validated if present)")
	verbose := fs.Bool("v", false, "print every case")
	_ = fs.Parse(args)

	rules, err := loadAndTest(*rulesDir, *verbose)
	if err != nil {
		return err
	}
	if _, err := os.Stat(*opmlPath); err == nil {
		feeds, err := LoadOPML(*opmlPath)
		if err != nil {
			return err
		}
		if err := checkKeepRefs(feeds, rules); err != nil {
			return err
		}
		fmt.Printf("opml: %d feeds, keep references valid\n", len(feeds))
	}
	return nil
}

func checkKeepRefs(feeds []OPMLFeed, rules Rules) error {
	var errs []string
	for _, f := range feeds {
		if f.Keep != "" {
			if _, ok := rules.FeedKeep[f.Keep]; !ok {
				errs = append(errs, fmt.Sprintf("%s: feedctlKeep=%q has no rules/%s.rules", f.URL, f.Keep, f.Keep))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	baseURL := fs.String("url", os.Getenv("MINIFLUX_URL"), "Miniflux base URL (default $MINIFLUX_URL)")
	rulesDir := fs.String("rules", "rules", "rules directory")
	opmlPath := fs.String("opml", "feeds/feeds.opml", "OPML feed list")
	dry := fs.Bool("dry-run", false, "print the plan, change nothing")
	_ = fs.Parse(args)

	if *baseURL == "" {
		return errors.New("MINIFLUX_URL is not set (http://miniflux:8080 in the stack, else the nginx URL)")
	}
	var client *Client
	if token := os.Getenv("MINIFLUX_TOKEN"); token != "" {
		client = NewClient(*baseURL, token)
	} else if user, pass := os.Getenv("MINIFLUX_USERNAME"), os.Getenv("MINIFLUX_PASSWORD"); user != "" && pass != "" {
		client = NewBasicClient(*baseURL, user, pass)
	} else {
		return errors.New("set MINIFLUX_TOKEN (Miniflux: Settings > API Keys) or MINIFLUX_USERNAME and MINIFLUX_PASSWORD")
	}
	rules, err := loadAndTest(*rulesDir, false)
	if err != nil {
		return fmt.Errorf("refusing to apply: %w", err)
	}
	feeds, err := LoadOPML(*opmlPath)
	if err != nil {
		return err
	}
	if err := checkKeepRefs(feeds, rules); err != nil {
		return err
	}

	return runApply(client, rules, feeds, *dry, os.Stdout)
}

// runApply syncs global rules, imports missing feeds and sets per-feed keep rules.
// Idempotent: a second run against an unchanged repo makes no write calls.
func runApply(c *Client, rules Rules, feeds []OPMLFeed, dry bool, w io.Writer) error {
	prefix := ""
	if dry {
		prefix = "[dry-run] "
	}

	// 1. Global rules: block list from the repo, keep list always empty.
	me, err := c.Me()
	if err != nil {
		return err
	}
	wantBlock := rules.GlobalBlock.Serialize()
	if me.BlockFilterEntryRules != wantBlock || me.KeepFilterEntryRules != "" {
		fmt.Fprintf(w, "%suser %q: set global block rules (%d), clear global keep rules\n", prefix, me.Username, len(rules.GlobalBlock.Rules))
		if !dry {
			if err := c.UpdateUser(me.ID, map[string]any{
				"block_filter_entry_rules": wantBlock,
				"keep_filter_entry_rules":  "",
			}); err != nil {
				return err
			}
		}
	} else {
		fmt.Fprintf(w, "user %q: global rules up to date\n", me.Username)
	}

	// 2. Import feeds missing from Miniflux (only those, so re-runs are idempotent).
	existing, err := c.Feeds()
	if err != nil {
		return err
	}
	byURL := map[string]Feed{}
	for _, f := range existing {
		byURL[f.FeedURL] = f
	}
	var missing []OPMLFeed
	for _, f := range feeds {
		if _, ok := byURL[f.URL]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "%simport %d new feed(s):\n", prefix, len(missing))
		for _, f := range missing {
			fmt.Fprintf(w, "  + [%s] %s\n", f.Category, f.URL)
		}
		if !dry {
			if err := c.ImportOPML(BuildOPML(missing)); err != nil {
				return err
			}
			if existing, err = c.Feeds(); err != nil {
				return err
			}
			for _, f := range existing {
				byURL[f.FeedURL] = f
			}
		}
	} else {
		fmt.Fprintln(w, "feeds: all present")
	}

	// 3. Per-feed keep rules for every feed listed in the OPML (empty if no feedctlKeep).
	for _, f := range feeds {
		mf, ok := byURL[f.URL]
		if !ok {
			if dry {
				continue // would be imported above
			}
			fmt.Fprintf(w, "warning: %s not found after import (URL rewritten by Miniflux?)\n", f.URL)
			continue
		}
		want := ""
		if f.Keep != "" {
			want = rules.FeedKeep[f.Keep].Serialize()
		}
		if mf.KeepFilterEntryRules == want {
			continue
		}
		label := "clear keep rules"
		if want != "" {
			label = "set keep rules " + f.Keep
		}
		fmt.Fprintf(w, "%sfeed %d %q: %s\n", prefix, mf.ID, mf.Title, label)
		if !dry {
			if err := c.UpdateFeed(mf.ID, map[string]any{"keep_filter_entry_rules": want}); err != nil {
				return err
			}
		}
	}
	extra := 0
	inOPML := map[string]bool{}
	for _, f := range feeds {
		inOPML[f.URL] = true
	}
	for _, f := range existing {
		if !inOPML[f.FeedURL] {
			extra++
		}
	}
	if extra > 0 {
		fmt.Fprintf(w, "note: %d feed(s) in Miniflux are not in the OPML; left untouched\n", extra)
	}
	return nil
}

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	opmlPath := fs.String("opml", "feeds/feeds.opml", "OPML feed list")
	timeout := fs.Duration("timeout", 20*time.Second, "per-request timeout")
	_ = fs.Parse(args)

	feeds, err := LoadOPML(*opmlPath)
	if err != nil {
		return err
	}
	type result struct {
		f      OPMLFeed
		status string
		kind   string
	}
	client := &http.Client{Timeout: *timeout}
	results := make([]result, len(feeds))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, f := range feeds {
		wg.Add(1)
		go func(i int, f OPMLFeed) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = result{f: f}
			req, _ := http.NewRequest("GET", f.URL, nil)
			req.Header.Set("User-Agent", "feedctl/1 (+self-hosted Miniflux)")
			resp, err := client.Do(req)
			if err != nil {
				results[i].status = "ERR " + err.Error()
				return
			}
			defer resp.Body.Close()
			results[i].status = fmt.Sprint(resp.StatusCode)
			results[i].kind = feedKind(io.LimitReader(resp.Body, 1<<20))
		}(i, f)
	}
	wg.Wait()

	sort.SliceStable(results, func(a, b int) bool { return results[a].f.Category < results[b].f.Category })
	bad := 0
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, r := range results {
		ok := r.status == "200" && r.kind != "" && r.kind != "not a feed"
		mark := "ok"
		if !ok {
			mark = "BAD"
			bad++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", mark, r.status, r.kind, r.f.Category, r.f.URL)
	}
	tw.Flush()
	fmt.Printf("%d feeds, %d bad\n", len(results), bad)
	if bad > 0 {
		return fmt.Errorf("%d feed(s) failed", bad)
	}
	return nil
}

// feedKind returns rss | atom | rdf by the document's root element.
func feedKind(r io.Reader) string {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return "not a feed"
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch strings.ToLower(se.Name.Local) {
			case "rss":
				return "rss"
			case "feed":
				return "atom"
			case "rdf":
				return "rdf"
			}
			return "not a feed"
		}
	}
}
