FROM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mc-console-bridge ./minecraft/bridge

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthprobe ./minecraft/cmd/healthprobe

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/mc-console-bridge /mc-console-bridge
COPY --from=build /out/healthprobe /healthprobe
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/healthprobe"]
ENTRYPOINT ["/mc-console-bridge"]
