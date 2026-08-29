FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -trimpath \
	-ldflags="-s -w \
	-X rssam/internal/version.Version=${VERSION} \
	-X rssam/internal/version.Commit=${COMMIT} \
	-X rssam/internal/version.Date=${DATE}" \
	-o /out/rssam ./cmd/rssam

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/rssam /rssam
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/rssam"]

