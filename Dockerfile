# syntax=docker/dockerfile:1.7

FROM golang:1.27.0-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=devel
ARG REVISION=unknown
ARG BUILD_DATE=unknown

ENV GOPRIVATE=github.com/dynamia-ai/*

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=secret,id=github_token,required=true \
    set -eu; \
    token="$(cat /run/secrets/github_token)"; \
    git config --global url."https://x-access-token:${token}@github.com/".insteadOf "https://github.com/"; \
    GOWORK=off go mod download; \
    rm -f "${HOME}/.gitconfig"

COPY *.go ./
COPY skills/ ./skills/
RUN --mount=type=secret,id=github_token,required=true \
    set -eu; \
    token="$(cat /run/secrets/github_token)"; \
    git config --global url."https://x-access-token:${token}@github.com/".insteadOf "https://github.com/"; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOWORK=off \
      go build -trimpath \
      -ldflags "-s -w -X main.cliVersion=${VERSION} -X main.cliRevision=${REVISION} -X main.cliBuildDate=${BUILD_DATE}" \
      -o /out/hami-cli .; \
    rm -f "${HOME}/.gitconfig"

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/hami-cli /usr/local/bin/hami-cli

ENTRYPOINT ["/usr/local/bin/hami-cli"]
