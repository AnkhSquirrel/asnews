package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Minimal Miniflux REST client. Field names verified against miniflux/v2
// internal/model (user.go, feed.go) and internal/api/api.go at v2.3.3.

type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

type User struct {
	ID                    int64  `json:"id"`
	Username              string `json:"username"`
	BlockFilterEntryRules string `json:"block_filter_entry_rules"`
	KeepFilterEntryRules  string `json:"keep_filter_entry_rules"`
}

type Feed struct {
	ID                    int64  `json:"id"`
	Title                 string `json:"title"`
	FeedURL               string `json:"feed_url"`
	BlockFilterEntryRules string `json:"block_filter_entry_rules"`
	KeepFilterEntryRules  string `json:"keep_filter_entry_rules"`
	Category              struct {
		Title string `json:"title"`
	} `json:"category"`
}

func (c *Client) do(method, path string, body []byte, contentType string, out any, ok ...int) error {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", c.token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if !slices.Contains(ok, resp.StatusCode) {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (c *Client) Me() (User, error) {
	var u User
	err := c.do("GET", "/v1/me", nil, "", &u, http.StatusOK)
	return u, err
}

// UpdateUser sends only the given fields (Miniflux uses pointer fields; omitted = unchanged).
func (c *Client) UpdateUser(id int64, fields map[string]any) error {
	return c.do("PUT", fmt.Sprintf("/v1/users/%d", id), c.jsonBody(fields), "application/json", nil,
		http.StatusOK, http.StatusCreated, http.StatusNoContent)
}

func (c *Client) Feeds() ([]Feed, error) {
	var feeds []Feed
	err := c.do("GET", "/v1/feeds", nil, "", &feeds, http.StatusOK)
	return feeds, err
}

func (c *Client) UpdateFeed(id int64, fields map[string]any) error {
	return c.do("PUT", fmt.Sprintf("/v1/feeds/%d", id), c.jsonBody(fields), "application/json", nil,
		http.StatusOK, http.StatusCreated, http.StatusNoContent)
}

func (c *Client) ImportOPML(opml []byte) error {
	return c.do("POST", "/v1/import", opml, "application/xml", nil,
		http.StatusOK, http.StatusCreated)
}
