# syntax=docker/dockerfile:1.7

FROM golang:1.27.0-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=devel
ARG REVISION=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download

COPY *.go ./
COPY internal/ ./internal/
COPY skills/ ./skills/
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOWORK=off \
      go build -trimpath \
      -ldflags "-s -w -X main.cliVersion=${VERSION} -X main.cliRevision=${REVISION} -X main.cliBuildDate=${BUILD_DATE}" \
      -o /out/hami-cli .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/hami-cli /usr/local/bin/hami-cli

ENTRYPOINT ["/usr/local/bin/hami-cli"]
