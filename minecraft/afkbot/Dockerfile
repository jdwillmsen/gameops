FROM golang:1.27-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build
WORKDIR /src

COPY go.mod go.sum ./
# No credentials needed here, deliberately. The four packages shared with
# minecraft-server-agent are copied into internal/ rather than imported: that
# module is private, so importing it would put a token in this build -- the
# build of the workload that keeps the farm's chunks loaded.
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# gophertunnel's RakNet implementation is pure Go, so the only runtime needs
# are the binary and TLS roots for the Xbox Live device-code login.
# distroless/static bundles CA certs and a non-root user without a shell.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/bot /bot

# The token cache. Losing it costs a device-code login per bot, so it is a
# volume rather than container-local state.
VOLUME ["/data"]
ENTRYPOINT ["/bot"]
