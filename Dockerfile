# feedctl image: the binary plus this repo's rules/ and feeds/, so `apply` pushes exactly what is
# committed. Built and published by .github/workflows/feedctl.yml; run by the `feedctl` service in
# deploy/compose.yaml.
#   docker build -t asnews-feedctl .        # also runs gofmt, go vet and go test
#   docker run --rm asnews-feedctl check -v

# Cross-compiles on the build machine for every target platform; no emulation needed.
FROM --platform=$BUILDPLATFORM golang:1.22 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY tools/feedctl tools/feedctl
COPY rules rules
COPY feeds feeds
# A rules regression (rules/cases.tsv) fails the build.
RUN test -z "$(gofmt -l tools/feedctl)" && go vet ./... && go test ./...
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags=-s -o /out/feedctl ./tools/feedctl

# Static, non-root; includes the CA certificates `probe` needs for HTTPS feeds.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /asnews
COPY --from=build /src/rules rules
COPY --from=build /src/feeds feeds
COPY --from=build /out/feedctl /usr/local/bin/feedctl
ENTRYPOINT ["/usr/local/bin/feedctl"]
CMD ["check"]
