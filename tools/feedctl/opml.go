package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strings"
)

// OPML conventions for this repo:
//   - top-level <outline text="Category"> groups feeds; Miniflux creates the category on import
//   - feed outlines carry xmlUrl; optional feedctlKeep="<keep-*.rules name>" marks a firehose
//     feed that gets those keep rules. Miniflux ignores the unknown attribute.
//
// Miniflux import behaviour (v2.3.3 internal/reader/opml/handler.go): feeds whose URL already
// exists are skipped, nothing is fetched at import time, and the category name is the parent
// outline's title, falling back to text. Miniflux also understands namespaced attributes
// (xmlns:miniflux="https://miniflux.app/opml", e.g. miniflux:keepFilterEntryRules) on import
// and export; feedctl does not use them so rules live only in rules/*.rules.

type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Keep     string    `xml:"feedctlKeep,attr"`
	Children []outline `xml:"outline"`
}

type opmlDoc struct {
	XMLName xml.Name `xml:"opml"`
	Head    struct {
		Title string `xml:"title"`
	} `xml:"head"`
	Body struct {
		Outlines []outline `xml:"outline"`
	} `xml:"body"`
}

type OPMLFeed struct {
	Title    string
	URL      string
	SiteURL  string
	Category string
	Keep     string
}

func LoadOPML(path string) ([]OPMLFeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc opmlDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var feeds []OPMLFeed
	var walk func(items []outline, category string)
	walk = func(items []outline, category string) {
		for _, o := range items {
			name := o.Title // same precedence as Miniflux: title, then text
			if name == "" {
				name = o.Text
			}
			if o.XMLURL != "" {
				feeds = append(feeds, OPMLFeed{Title: name, URL: strings.TrimSpace(o.XMLURL), SiteURL: o.HTMLURL, Category: category, Keep: o.Keep})
			}
			if len(o.Children) > 0 {
				walk(o.Children, name)
			}
		}
	}
	walk(doc.Body.Outlines, "")

	seen := map[string]bool{}
	var errs []string
	for _, f := range feeds {
		if seen[f.URL] {
			errs = append(errs, "duplicate feed URL: "+f.URL)
		}
		seen[f.URL] = true
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "\n"))
	}
	return feeds, nil
}

// BuildOPML renders feeds grouped by category, in input order, for POST /v1/import.
func BuildOPML(feeds []OPMLFeed) []byte {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<opml version="2.0"><head><title>feedctl import</title></head><body>` + "\n")
	var order []string
	groups := map[string][]OPMLFeed{}
	for _, f := range feeds {
		if _, ok := groups[f.Category]; !ok {
			order = append(order, f.Category)
		}
		groups[f.Category] = append(groups[f.Category], f)
	}
	for _, cat := range order {
		if cat != "" {
			fmt.Fprintf(&b, `<outline text="%s">`+"\n", xmlEscape(cat))
		}
		for _, f := range groups[cat] {
			fmt.Fprintf(&b, `<outline type="rss" text="%s" title="%s" xmlUrl="%s" htmlUrl="%s"/>`+"\n",
				xmlEscape(f.Title), xmlEscape(f.Title), xmlEscape(f.URL), xmlEscape(f.SiteURL))
		}
		if cat != "" {
			b.WriteString("</outline>\n")
		}
	}
	b.WriteString("</body></opml>\n")
	return []byte(b.String())
}

// xmlEscape escapes text for use inside a double-quoted attribute (EscapeText covers & < > " ').
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
