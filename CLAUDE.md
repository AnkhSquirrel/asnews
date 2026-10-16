# devfeed

Centralized developer news, self-hosted on the owner's Raspberry Pi 4 (8 GB) in an existing Docker environment.
devfeed uses two services of the separate `network-core` compose stack: `nginx` (reverse proxy) and `dnsmasq` (LAN DNS),
over the shared external Docker network `proxy`. network-core also runs `wireguard`; devfeed does not use it (no VPN).
Stack: Miniflux + Postgres (Docker Compose), with filter rules and the feed list versioned in this repo
and pushed to Miniflux by `feedctl` (Go, stdlib only). No AI/LLM anywhere.

Requirement: one reader for RSS, newsletters and aggregators. Exclude unwanted ecosystems everywhere.
For broad "firehose" feeds, keep only items from followed ecosystems. Everything else of general dev/FOSS/tech interest stays.
The current rules (block C#/.NET everywhere, keep only Java/JVM on firehose feeds) are **starter examples**;
the owner will replace and tune them. Don't treat specific patterns as fixed requirements.

Background, alternatives and sources: `docs/research.md`. Do not redo that research; extend it.

## Layout

```
deploy/compose.yaml                Miniflux 2.3.3 (pinned) + Postgres 18; RSSHub behind profile "rsshub"; joins network-core
deploy/.env.example                copy to deploy/.env (git-ignored)
deploy/nginx-devfeed.conf.example  server block to add to network-core's nginx
rules/global-block.rules           -> Miniflux user `block_filter_entry_rules` (all feeds)
rules/keep-*.rules                 -> per-feed `keep_filter_entry_rules`, only on OPML feeds tagged feedctlKeep="<name>"
rules/cases.tsv                    regression cases: expect, profile, title, tags, url, note
feeds/feeds.opml                   feed list; top-level outlines = categories
tools/feedctl/                     check | apply | probe; mirrors Miniflux filter semantics; tests incl. fake-API apply test
tools/minifluxcheck/               runs cases.tsv through Miniflux's real filter.go at a tag (separate Go module, Go >= 1.24)
docs/research.md                   findings, decision log, verified Miniflux internals, source links
```

## Commands

```sh
go test ./...                                  # must pass before any commit
go run ./tools/feedctl check -v                # rules + cases table + OPML keep references
go run ./tools/feedctl probe                   # fetch every feed URL (run on the Pi or a network that reaches them)
MINIFLUX_URL=... MINIFLUX_TOKEN=... go run ./tools/feedctl apply -dry-run
MINIFLUX_URL=... MINIFLUX_TOKEN=... go run ./tools/feedctl apply
tools/minifluxcheck/run.sh [tag]               # after any Miniflux version bump, before changing MINIFLUX_VERSION
cd deploy && docker compose config --quiet     # validate compose after edits (needs deploy/.env)
```

`MINIFLUX_URL` is the nginx URL (Miniflux publishes no host port). `feedctl apply` has no default for it.

## Decisions (keep unless new evidence; record changes in docs/research.md)

1. Reader: Miniflux. FreshRSS only marks filtered items read; Tiny Tiny RSS's original infrastructure is gone (fork only); hosted services fail the requirement.
2. The repo is the source of truth.
   - `apply` overwrites the global block rules and clears global keep rules.
   - It sets or clears keep rules on every feed listed in the OPML. Edits made in the Miniflux UI to those settings are reverted on the next apply.
   - Feeds not in the OPML are left untouched. Removing a feed from the OPML does not delete it from Miniflux.
3. **No global keep rules, ever.** Miniflux merges user and feed keep lists, so a global keep rule drops every non-matching entry from every feed. `LoadRules` refuses `rules/global-keep.rules`.
4. Exclusion goes in `global-block.rules`. Inclusion means subscribing to ecosystem sources, plus `keep-*.rules` on firehose feeds only.
5. **Filtering is deterministic only. No AI/LLM, now or later** (no classifier, no hosted or local model). Record keyword misses as `KNOWN FALSE POSITIVE` / `KNOWN FALSE NEGATIVE` rows in `cases.tsv`, then fix the rules or accept the miss.
6. Newsletters: use native RSS first (TLDR has it). Otherwise hosted kill-the-newsletter.com, or kill-the-news on Cloudflare Workers. Do not run an SMTP receiver on the home Pi (ISPs block port 25).
7. The Miniflux image is pinned. Bump deliberately: read the release notes (the Miniflux releases feed is subscribed), run `tools/minifluxcheck/run.sh <new-tag>`, then update `MINIFLUX_VERSION`.
8. Hosting: devfeed is its own compose project (`name: devfeed`) next to `network-core`.
   - Only Miniflux joins the external network `proxy`, with alias `devfeed-miniflux`. Never put `db` or `rsshub` on `proxy`: their service names could collide with other stacks.
   - network-core's `nginx` proxies by hostname on its existing 80/443 to `http://devfeed-miniflux:8080` and terminates TLS (certs in its `./certs`). `dnsmasq` serves the hostname.
   - **No VPN.** devfeed never uses network-core's `wireguard`, for ingress or egress: no `network_mode: service:wireguard`, and feeds are fetched directly. Access is from the LAN.
   - No host ports. Other stacks on the Pi publish their own; asnews needs none.

## Miniflux facts that bite

Verified from source, 2.3.3 and main @ c317f84. Details in `docs/research.md`.

- **No comment syntax in rules.** `EntryTitle=(?i)foo   # note` silently never matches; the official docs examples show that broken style. Full-line comments are harmless. `feedctl` strips them and rejects inline ones.
- Invalid regexes and unknown fields are silently ignored by Miniflux. `feedctl check` compiles every rule and fails on them.
- Regexes are RE2 (Go `regexp`): no lookaround, no backreferences.
- Order: user block, feed block, user keep, feed keep. Block always beats keep.
- `EntryTag` is matched per tag (anchors are safe). `EntryDate=max-age:Nd` matches entries *older* than N days.
- API fields `block_filter_entry_rules` / `keep_filter_entry_rules` exist on both user (`PUT /v1/users/{id}`) and feed (`PUT /v1/feeds/{id}`). They are missing from the API docs page but present in the model.
- `POST /v1/import` skips existing feed URLs and fetches nothing. Category = parent outline title, falling back to text. Namespaced `miniflux:*` OPML attributes (incl. `miniflux:keepFilterEntryRules`) are supported on import/export but unused here.

## Verification status

- Done:
  - `go test ./...` green (Go 1.22).
  - 27 cases (covering the starter example rules) pass in `feedctl` and through Miniflux's own `filter.go` at 2.3.3 and main, both with comments stripped and with raw files.
  - `apply` tested against a fake API: payloads, import of missing feeds only, dry-run makes no writes, second run makes no writes.
  - Compose (network-core variant) validated with `docker compose config`, with and without profile `rsshub`.
- Not done:
  - **No live Miniflux was run.** The first live `apply` must be `-dry-run`.
  - **Feed URLs were not fetched.** Lobsters, Spring, Reddit, LWN and GitHub release URLs come from general knowledge; run `probe` on the Pi first.
  - **Host facts known:**
    - Raspberry Pi 4, 8 GB RAM, static LAN IP.
    - network-core compose: containers `nginx`, `dnsmasq` and `wireguard`, all on the external network `proxy`. TZ Europe/Paris.
  - **Host facts unknown:** CPU arch (`uname -m`; network-core's comments target arm64), Docker/Compose versions, devfeed hostname, certificate type in `./certs`, storage medium (SD/SSD) and location, backup setup.

## Working rules

- Rule change workflow:
  1. Edit the `.rules` file.
  2. Add at least one `block` and one `keep` row to `cases.tsv` covering the new pattern and its nearest false-positive risk.
  3. Run `go test ./...`.
  4. Run `apply -dry-run`, then `apply`.
- New keep profile: `rules/keep-<name>.rules` plus cases with profile `keep-<name>`, then tag feeds with `feedctlKeep="keep-<name>"`.
- Rules files: one rule per line; comments only on their own lines.
- Go:
  - `tools/feedctl` is stdlib only, Go 1.22-compatible, gofmt and go vet clean.
  - Keep `rules.go`'s mirror of Miniflux semantics in sync on version bumps (diff `internal/reader/filter/filter.go` between tags).
- Commits: always follow [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).
  - Format: `<type>[optional scope]: <description>`, optional body, optional footers.
  - Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `build`, `ci`, `perf`, `style`, `revert`.
  - Scopes when useful: `rules`, `feeds`, `feedctl`, `minifluxcheck`, `deploy`, `docs`.
  - Breaking changes: `!` after type/scope and/or a `BREAKING CHANGE:` footer.
- Secrets: never commit `deploy/.env` or API tokens. `MINIFLUX_TOKEN` comes from Miniflux > Settings > API Keys.
- Publish no host ports. Miniflux is reached only through network-core's nginx; Postgres and RSSHub are not reachable from outside the stack.

## Roadmap

Phase 1, deploy:
1. Collect the unknown host facts (see Verification status) and pick the devfeed hostname. Adapt the volume to the storage location if needed.
2. Fill `deploy/.env` (incl. `MINIFLUX_BASE_URL`), run `docker compose up -d` (network-core must be up).
3. In network-core, per `deploy/nginx-devfeed.conf.example`:
   - Paste the server block into `nginx.conf`, then `nginx -t` and reload.
   - Add an `address=` line to `dnsmasq.conf`, then restart `dnsmasq`.
   - Add TLS.
   Then create an API key.
4. Run `feedctl probe` on the Pi. Fix or remove dead URLs.
5. Run `feedctl apply -dry-run`, then `apply`. Trigger a refresh with `PUT /v1/feeds/refresh`. Spot-check that blocked items are absent and that keep-profile feeds hold only kept items.
6. Enable the Google Reader API (Settings > Integrations) for a mobile client on the LAN.
7. Backups: nightly `pg_dump` of the miniflux DB, plus `GET /v1/export` (OPML; includes per-feed rules).

Phase 2, sources and tuning:
- Replace the starter example rules: after a week of reading, record misses as `cases.tsv` rows, then fix the rules.
- RSSHub routes for sites without RSS; inside compose, subscribe as `http://rsshub:1200/<route>`. Browser-based routes don't work on ARM.
- Newsletters without RSS, per decision 6.
- Optional: `feedctl simulate`, which fetches the live feeds and replays the rules locally to show what would be blocked and by which rule. Miniflux never stores blocked entries, so the API cannot show them.
