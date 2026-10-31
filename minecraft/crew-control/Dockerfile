FROM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
WORKDIR /src

COPY go.mod go.sum ./
# Built from the repo root, where the one module's files live; the service
# imports the agent's pkg/ in place rather than carrying copies.
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/crew-control ./minecraft/crew-control/cmd/crew-control

# distroless/static bundles a non-root user and no shell; the service holds
# no state, so there is no volume.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/crew-control /crew-control

# Stated so a scanner that cannot resolve a digest-pinned base image's USER
# still sees a non-root image.
USER nonroot:nonroot

EXPOSE 8080
ENTRYPOINT ["/crew-control"]
