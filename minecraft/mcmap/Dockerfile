FROM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
WORKDIR /src

# Built from the repo root, where the one module's files live.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcmap ./minecraft/mcmap/cmd/mcmap

# distroless/cc rather than /static: the service itself is a static binary,
# but the renderer it downloads at runtime is a native executable that needs
# glibc and libgcc. That renderer is deliberately not in this image; its
# licence allows free use but not redistribution.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthprobe ./minecraft/cmd/healthprobe

FROM gcr.io/distroless/cc-debian12:nonroot@sha256:9dac0a79194e45a7da0158a9c6da57b217585af0786db3845d1f0ec1a0dd182f
COPY --from=build /out/mcmap /mcmap
COPY --from=build /out/healthprobe /healthprobe

# Redundant at runtime -- the `:nonroot` base already runs as uid 65532 -- but
# a scanner reading this file cannot resolve a digest-pinned base image's USER,
# so without the line it reports the image as running root.
USER nonroot:nonroot

# The world mirror, the rendered tiles and the installed renderer. All of it
# can be rebuilt from the server, but the first render is slow, so it is a
# volume rather than container-local state.
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/healthprobe"]
ENTRYPOINT ["/mcmap"]
