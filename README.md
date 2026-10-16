# asnews

Self-hosted developer news on a Raspberry Pi 4, served through the nginx of the existing
`network-core` Docker stack. It runs Miniflux with deterministic filter rules kept in git (no AI/LLM).
The starter rules exclude C#/.NET everywhere and keep only JVM-related items on firehose feeds;
they are examples to be replaced and tuned.

```sh
# on the Pi
cd deploy && cp .env.example .env && $EDITOR .env   # incl. MINIFLUX_BASE_URL; network-core must be up
docker compose up -d                       # add --profile rsshub for RSSHub
# then add nginx-asnews.conf.example to network-core's nginx

# anywhere with Go >= 1.22
go test ./...                              # rules vs rules/cases.tsv, OPML, apply logic
go run ./tools/feedctl check -v            # same, with a per-case table
go run ./tools/feedctl probe               # are the feed URLs alive? (run where they are reachable)
export MINIFLUX_URL=https://<asnews host> MINIFLUX_TOKEN=...
go run ./tools/feedctl apply -dry-run      # plan
go run ./tools/feedctl apply               # push rules + feeds
```

Background and decisions: `docs/research.md`. Working agreement for Claude Code: `CLAUDE.md`.
