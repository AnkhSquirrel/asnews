# asnews

Self-hosted developer news on a Raspberry Pi 4, served through the nginx of the existing
`network-core` Docker stack. It runs Miniflux with deterministic filter rules kept in git (no AI/LLM).
The starter rules exclude C#/.NET everywhere and keep only JVM-related items on firehose feeds;
they are examples to be replaced and tuned.

Everything runs in Docker. CI tests the rules and publishes a `feedctl` image with the rules and
feed list built in. On every `up`, a one-shot `feedctl` container pushes them to Miniflux.

```sh
# on the Pi: only compose.yaml and .env, no repo checkout (network-core must be up)
mkdir -p ~/asnews && cd ~/asnews
curl -fsSLO https://raw.githubusercontent.com/AnkhSquirrel/asnews/main/deploy/compose.yaml
curl -fsSL -o .env https://raw.githubusercontent.com/AnkhSquirrel/asnews/main/deploy/.env.example
$EDITOR .env                                   # incl. MINIFLUX_BASE_URL
docker compose pull && docker compose up -d --force-recreate   # add --profile rsshub for RSSHub
docker compose logs feedctl                    # what apply did (first run: dry run, see .env)
docker compose run --rm --no-deps feedctl probe   # are the feed URLs alive?
# then add nginx-asnews.conf.example to network-core's nginx
```

To change rules or feeds: edit, commit and push. Once CI has published the image, run
`docker compose pull && docker compose up -d --force-recreate` on the Pi.

```sh
# development (Go >= 1.22, or the golang:1.22 image)
go test ./...                              # rules vs rules/cases.tsv, OPML, apply logic
go run ./tools/feedctl check -v            # same, with a per-case table
docker build -t asnews-feedctl .           # the image, as CI builds it
```

Background and decisions: `docs/research.md`. Working agreement for Claude Code: `CLAUDE.md`.
