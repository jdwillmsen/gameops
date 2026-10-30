FROM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
WORKDIR /src

# Built from the repo root: the whole monorepo is one Go module, so the
# module files live there, not beside this Dockerfile.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agent ./minecraft/agent/cmd/agent
# One codebase, one image: census runs as a scheduled job beside the
# server rather than inside the agent process, so its binary rides along
# here instead of getting a Dockerfile of its own.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/census ./minecraft/agent/cmd/census
# Same reasoning as census: the probe speaks the same Bedrock handshake this
# module already implements, so it rides here rather than growing a second
# image to pin, pull and keep in step with the protocol code it shares.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/joinprobe ./minecraft/agent/cmd/joinprobe

# gophertunnel's RakNet and NetherNet implementations are pure Go - no cgo, no native
# addon - so the only runtime requirement is the binary and TLS roots for
# the Xbox Live device-code login. distroless/static bundles CA certs and a
# non-root user without a shell, which a bare `FROM scratch` cannot offer
# without vendoring the cert bundle by hand.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthprobe ./minecraft/cmd/healthprobe

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/agent /agent
COPY --from=build /out/healthprobe /healthprobe
COPY --from=build /out/census /census
COPY --from=build /out/joinprobe /joinprobe

# Redundant at runtime -- the `:nonroot` base already runs as uid 65532 -- but
# a scanner reading this file cannot resolve a digest-pinned base image's USER,
# so without the line it reports the image as running root. Stating it makes
# the property checkable from the Dockerfile alone, and survives a future base
# image change that quietly reverts to root.
USER nonroot:nonroot

VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/healthprobe"]
ENTRYPOINT ["/agent"]
