# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG MAIN_PACKAGE=./cmd/controller

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/schedule-autoscaler "${MAIN_PACKAGE}"

FROM scratch

COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/schedule-autoscaler /schedule-autoscaler

USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/schedule-autoscaler"]
