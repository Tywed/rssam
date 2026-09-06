# syntax=docker/dockerfile:1
# Multi-arch build: `docker buildx build --platform linux/amd64,linux/arm64 .`
# The build stage always runs natively (--platform=$BUILDPLATFORM) and
# cross-compiles for the requested target; Go needs no emulation for that.
# Base images are pinned by digest (dependabot's docker ecosystem bumps them).
FROM --platform=$BUILDPLATFORM golang:1.27.1@sha256:512690a5660563b57d37ecc31129e7f136e831db2aed24a1dbeb8ad7380dc0fa AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath \
	-ldflags="-s -w -buildid= \
	-X rssam/internal/version.Version=${VERSION} \
	-X rssam/internal/version.Commit=${COMMIT} \
	-X rssam/internal/version.Date=${DATE}" \
	-o /out/rssam ./cmd/rssam

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /
COPY --from=build /out/rssam /rssam
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/rssam"]
