FROM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
WORKDIR /src

COPY go.mod go.sum ./
# Built from the repo root, where the one module's files live. The packages
# shared with the agent are copies under internal/;
# minecraft/afkbot/docs/decisions.md has why.
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/bot ./minecraft/afkbot/cmd/bot

# gophertunnel's RakNet and NetherNet (pion WebRTC) implementations are pure Go, so the only runtime needs
# are the binary and TLS roots for the Xbox Live device-code login.
# distroless/static bundles CA certs and a non-root user without a shell.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/bot /bot

# Redundant at runtime -- the `:nonroot` base already runs as uid 65532 -- but
# a scanner reading this file cannot resolve a digest-pinned base image's USER,
# so without the line it reports the image as running root. Stating it makes
# the property checkable from the Dockerfile alone, and survives a future base
# image change that quietly reverts to root.
USER nonroot:nonroot

# The token cache. Losing it costs a device-code login per bot, so it is a
# volume rather than container-local state.
VOLUME ["/data"]
ENTRYPOINT ["/bot"]
