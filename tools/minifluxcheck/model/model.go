// Package model stubs the fields that miniflux internal/reader/filter reads, so the
// upstream filter.go can be compiled verbatim without the rest of Miniflux.
// If a Miniflux upgrade makes filter.go read new fields, add them here.
package model

import "time"

type Feed struct {
	FeedURL        string
	BlocklistRules string
	KeeplistRules  string
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
