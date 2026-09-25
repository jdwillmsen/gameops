FROM golang:1.27-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build
WORKDIR /src

COPY go.mod go.sum ./
# The packages shared with minecraft-server-agent are copies under internal/.
# The one module this fetches from that repository is its standard-library-only
# presence contract; docs/decisions.md has why each is handled the way it is.
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/bot ./cmd/bot

# gophertunnel's RakNet implementation is pure Go, so the only runtime needs
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
