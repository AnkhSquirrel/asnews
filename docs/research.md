# Research: centralized developer news with include/exclude filtering

Date: 2026-10-15. Prices and features change; re-check before relying on them.

## Requirement

One place for developer news: RSS feeds, newsletters (e.g. TLDR), aggregators (HN, Lobsters).
Filtering by interest/language/ecosystem, e.g. exclude C#/.NET, include the Java/JVM ecosystem.
Host: a Raspberry Pi 4 (8 GB) running Docker. An existing `network-core` compose stack runs
nginx, dnsmasq and wireguard on the external network `proxy`; asnews runs as a
separate compose project behind that nginx and uses its dnsmasq, but not the VPN.

## Verdict

No hosted service satisfies centralization + deterministic include/exclude filtering.
The products that filter well are closed silos. The ones that ingest arbitrary
sources filter by paid keyword rules or opaque AI. Self-hosted Miniflux covers the
requirement with deterministic rules; the only custom code is `feedctl`, which validates
and pushes those rules.

## Hosted options

| Service | Filtering | Ingests your own sources | Gap |
|---|---|---|---|
| daily.dev | Follow/block tags, block sources, content categories, content language | No | Blocking applies only to "My Feed"; no RSS export found; Public API is a Plus feature |
| Inoreader Pro (~$7.50/mo yearly) | 30 rules, 50 filters, duplicate filters; bring-your-own-AI since April 2026 | Yes (RSS, newsletters, 20 web feeds for sites without RSS) | Paid; keyword-level; no daily.dev content |
| Feedly Pro+ (~$12/mo yearly; sources disagree, $99–144/yr) | AI mute filters, deduplication | Yes | Opaque AI filtering; users report many AI features reserved for Enterprise |

## Self-hosted reader core

- **Miniflux (chosen).** Go plus PostgreSQL. Official images for amd64, arm64, arm/v7 and arm/v6. Global (user) and per-feed block/keep rules since 2.2.10, using RE2 regex on title, URL, comments URL, content, author, tag and date. Blocked entries are never stored. Has a REST API, webhooks, and Fever/Google Reader APIs for mobile clients.
- **FreshRSS.** PHP, works with SQLite. Filter actions use PCRE2 regex but *mark as read* rather than drop. 1.28 can apply filter actions to existing articles. A valid alternative.
- **Tiny Tiny RSS.** Not chosen. The original developer shut down tt-rss.org infrastructure on 2025-11-01; a community fork continues on GitHub.

## Miniflux filter engine, verified from source

Source: `internal/reader/filter/filter.go` and `internal/reader/processor/processor.go`, tag 2.3.3 and main @ c317f84.

- One `Field=Regex` rule per line, split on the first `=`, both sides trimmed. Regexes are RE2 (Go `regexp`).
- **No comment syntax.** `EntryTitle=(?i)miniflux   # comment` never matches, because the comment becomes part of the regex. Confirmed by running the real filter code. The official docs show this style in their examples. Full-line comments are harmless: they parse as an unknown field and never match.
- Invalid regexes and unknown fields are silently ignored.
- `blockRules = ParseRules(user.Block, feed.Block)` and `allowRules = ParseRules(user.Keep, feed.Keep)`: user and feed lists are **merged**.
- Evaluation order: block list first (any match drops the entry). Then, if the merged keep list is non-empty, entries that match none of it are dropped.
  - A global keep rule therefore restricts every feed.
- `EntryTag` is matched against each tag separately, so `^...$` anchors work per tag.
- `EntryDate=max-age:30d` matches entries *older* than 30 days.
- Filters run again after content extraction (crawler) replaces the content.
- API JSON fields: user `block_filter_entry_rules` / `keep_filter_entry_rules` (`PUT /v1/users/{id}`, not in the API docs page but present in the model). Feed fields have the same names (`PUT /v1/feeds/{id}`). Auth header `X-Auth-Token`.
- OPML import (`POST /v1/import`):
  - Skips feed URLs that already exist and fetches nothing.
  - Category = parent outline title, falling back to text.
  - Also accepts `xmlns:miniflux="https://miniflux.app/opml"` attributes such as `miniflux:keepFilterEntryRules`.
- Webhook:
  - Events: `new_entries` (a `feed` object plus an `entries` array) and `save_entry`.
  - Headers: `X-Miniflux-Signature`, the hex HMAC-SHA256 of the raw body keyed with the webhook secret, and `X-Miniflux-Event-Type`.
  - Any response status of 400 or above is logged as an error.

## Sources (feeding the reader)

- **Hacker News via hnrss.org.**
  - `q=` takes keywords joined with ` OR ` and searches titles only by default.
  - `points=N` / `comments=N` set thresholds; `count` is at most 100.
  - Exclusion syntax is not documented, so do exclusion in Miniflux.
- **Lobsters:** `https://lobste.rs/rss`, per-tag feeds `https://lobste.rs/t/<tag>.rss`. Not verifiable from the research environment.
- **TLDR:** native RSS per vertical, `https://tldr.tech/api/rss/{webdev,devops,ai,infosec,data,...}`. The main "TLDR Tech" edition has no native feed.
- **Java ecosystem:** `https://feed.infoq.com/java`, `https://inside.java/feed.xml`, `https://foojay.io/feed/`.
- **GitHub releases:** `https://github.com/<owner>/<repo>/releases.atom`.
- **RSSHub** (sites without RSS): thousands of routes. The arm/arm64 image lacks puppeteer, so browser-based routes fail on the Pi.
- **Other newsletters:**
  - Hosted kill-the-newsletter.com, or kill-the-news (self-hosted on Cloudflare Workers with your own domain).
  - Self-hosting an SMTP receiver at home is impractical: residential ISPs usually block inbound port 25.

## Semantic layer (rejected 2026-10-16: no AI/LLM)

Kept as research only; see the decision log. Keyword rules cannot disambiguate "Spring"
(season), "Java" (island) or "Go" (verb); such misses are recorded as `KNOWN FALSE POSITIVE` /
`KNOWN FALSE NEGATIVE` rows in `rules/cases.tsv` and either fixed in the rules or accepted.

- **feeds.fun:** LLM tagging plus scoring rules over tags. OpenAI/Gemini or any OpenAI-compatible endpoint. Python + Postgres; the default setup loads spaCy `en_core_web_lg`, which is heavy for a Pi.
- **Newscope:** LLM relevance score 0–10 with preferred/avoided topics. Serves filtered RSS (`/rss?min_score=X`, `/rss/{topic}`) and could sit in front of Miniflux.
- **betternews:** Ollama-based scoring, default model `llama3.2:3b`.
- **Custom:** Miniflux webhook → classifier → Miniflux API (mark read / star). A spec existed (`docs/classifier-spec.md`) and was deleted with this decision.
- **Pi constraint:** local inference over hundreds of items a day is marginal on a Pi. Use a remote OpenAI-compatible endpoint, or Ollama on a stronger machine.

## Decision log

### 2026-10-16

- **No AI/LLM.** Filtering stays deterministic (Miniflux rules managed by `feedctl`). Phase 3
  (semantic classifier) is dropped and `docs/classifier-spec.md` removed. The webhook facts above
  remain as Miniflux internals.
- **Host and hosting.** Raspberry Pi 4, 8 GB. asnews is its own compose project next to the
  existing `network-core` stack.
  - network-core runs `nginx`, `dnsmasq` and `wireguard`, all on the external network `proxy`,
    created with `docker network create proxy`.
  - asnews:
    - Miniflux joins `proxy` under the alias `asnews-miniflux`. nginx proxies to it by hostname on
      the existing 80/443, so network-core's compose needs no change; only `nginx.conf` and
      `dnsmasq.conf` do.
    - **No VPN** (owner's decision): asnews never uses `wireguard`, for ingress or egress.
      Miniflux fetches feeds directly, and access is from the LAN through nginx + dnsmasq.
    - No host ports are published, replacing an earlier loopback binding that collided with a
      port already published on the Pi.
    - For the same reason, `feedctl apply` has no localhost default for `MINIFLUX_URL`: on the Pi,
      that port belongs to another service, which would have received the API token.
  - 8 GB RAM leaves room for RSSHub (profile `rsshub`).
- **Rules are starter examples.** The C#/.NET block rules, the JVM keep profile and their cases
  demonstrate the mechanism; the owner will replace and tune them after real reading.
- **Codename `asnews`.** Renamed from `devfeed`. This covers the compose project (`name: asnews`),
  the network alias `asnews-miniflux`, the Go module and `deploy/nginx-asnews.conf.example`.
- **Pi facts.** Raspberry Pi OS, `aarch64`, Docker >= 29.9.1, Docker Compose >= v5.6.0
  (versions at setup; they will be upgraded). Storage is the Pi's SD card, so the
  Postgres volume sits on the card. SD cards wear and can corrupt on power loss. `CLEANUP_ARCHIVE_READ_DAYS=30` already limits database growth.
- **No backups for now** (owner's decision). Feeds and rules are in git, and `docker compose up`
  plus `feedctl apply` rebuilds the instance. Losing the database or the SD card only loses
  Miniflux state: read/starred status, entries older than what the feeds still serve, UI settings
  and API keys. Revisit if starred entries start to matter (`pg_dump`, or `GET /v1/export` for OPML).

## Source links

- daily.dev blocking: https://docs.daily.dev/blocking-tags-sources/
- daily.dev advanced filtering: https://docs.daily.dev/advanced-filtering-options/
- daily.dev Plus / Public API: https://docs.daily.dev/plus-overview/ , https://docs.daily.dev/public-api/
- Feedly vs Inoreader (2026-09): https://feeder.co/knowledge-base/rss-basics/feedly-vs-inoreader/
- Feeder vs Inoreader (2026-09): https://feeder.co/knowledge-base/rss-basics/feeder-vs-inoreader/
- Inoreader pricing 2026: https://www.readless.app/blog/inoreader-pricing-2026
- Miniflux rules: https://miniflux.app/docs/rules.html
- Miniflux Docker: https://miniflux.app/docs/docker.html
- Miniflux API: https://miniflux.app/docs/api.html
- Miniflux webhooks: https://miniflux.app/docs/webhooks.html
- Miniflux source: https://github.com/miniflux/v2
- FreshRSS releases: https://github.com/FreshRSS/FreshRSS/releases
- TT-RSS fork: https://linuxiac.com/tt-rss-shuts-down-but-the-project-lives-on-under-a-new-fork
- hnrss: https://hnrss.github.io/
- RSSHub deploy: https://docs.rsshub.app/deploy
- kill-the-news: https://github.com/juherr/kill-the-news
- TLDR feeds: https://feeder.co/discover/site/tldr.tech
- Java feeds: https://rss.feedspot.com/java_rss_feeds/ , https://foojay.io/who-we-are/
- feeds.fun: https://github.com/Tiendil/feeds.fun
- Newscope: https://github.com/umputun/newscope
- betternews: https://github.com/sxntixgo/betternews
