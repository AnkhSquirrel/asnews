// Command minifluxcheck runs rules/cases.tsv through Miniflux's own filter code
// (internal/reader/filter/filter.go copied verbatim by run.sh) instead of feedctl's mirror.
// Run via: tools/minifluxcheck/run.sh [miniflux-tag]
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minifluxcheck/filter"
	"minifluxcheck/model"
)

func readRules(path string, stripComments bool) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !stripComments {
		return string(b)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

// run evaluates every case; stripComments=false simulates pasting the raw file into the UI.
func run(root string, stripComments bool) int {
	block := readRules(filepath.Join(root, "rules/global-block.rules"), stripComments)
	keep := map[string]string{}
	paths, _ := filepath.Glob(filepath.Join(root, "rules/keep-*.rules"))
	for _, p := range paths {
		keep[strings.TrimSuffix(filepath.Base(p), ".rules")] = readRules(p, stripComments)
	}

	f, err := os.Open(filepath.Join(root, "rules/cases.tsv"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()

	fails, n, line := 0, 0, 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "expect\t") {
			continue
		}
		c := strings.Split(raw, "\t")
		for len(c) < 6 {
			c = append(c, "")
		}
		e := &model.Entry{Title: strings.TrimSpace(c[2]), URL: strings.TrimSpace(c[4])}
		for _, t := range strings.Split(c[3], ",") {
			if t = strings.TrimSpace(t); t != "" {
				e.Tags = append(e.Tags, t)
			}
		}
		profile := strings.TrimSpace(c[1])
		feedKeep := ""
		if profile != "none" {
			k, ok := keep[profile]
			if !ok {
				fmt.Printf("FAIL line %d: unknown profile %q\n", line, profile)
				fails++
				continue
			}
			feedKeep = k
		}
		blocked := filter.IsBlockedEntry(
			filter.ParseRules(block, ""),    // user block rules
			filter.ParseRules("", feedKeep), // feed keep rules; user keep is always empty
			&model.Feed{FeedURL: "case"}, e)
		got := "keep"
		if blocked {
			got = "block"
		}
		n++
		if want := strings.TrimSpace(c[0]); got != want {
			fails++
			fmt.Printf("FAIL line %d [%s] %q: want %s, got %s\n", line, profile, e.Title, want, got)
		}
	}
	fmt.Printf("miniflux filter.go, comments stripped=%v: %d cases, %d failed\n", stripComments, n, fails)
	return fails
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: minifluxcheck <repo-root>")
		os.Exit(2)
	}
	if run(os.Args[1], true)+run(os.Args[1], false) > 0 {
		os.Exit(1)
	}
}
