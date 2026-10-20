FROM golang:1.27-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
WORKDIR /src

COPY go.mod go.sum ./
# Built from the repo root, where the one module's files live. Crew imports
# the agent's pkg/ in place rather than carrying copies;
# minecraft/crew/docs/decisions.md has why.
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/crew ./minecraft/crew/cmd/crew
# The nightly lab rebuild job runs this in an init container to write its
# plans and scripts into a volume the kubectl container reads: the runtime
# image has no shell or cp to do it.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/labcourses ./minecraft/crew/cmd/labcourses
# The runtime image has no shell to create or chown a directory, so the data
# directory is made here and copied across with its owner.
RUN mkdir /out/data

# gophertunnel's RakNet implementation is pure Go, so the only runtime needs
# are the binary and TLS roots for the Xbox Live device-code login.
# distroless/static bundles CA certs and a non-root user without a shell.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/crew /crew
COPY --from=build /out/labcourses /labcourses

# Redundant at runtime -- the `:nonroot` base already runs as uid 65532 -- but
# a scanner reading this file cannot resolve a digest-pinned base image's USER,
# so without the line it reports the image as running root. Stating it makes
# the property checkable from the Dockerfile alone, and survives a future base
# image change that quietly reverts to root.
USER nonroot:nonroot

# The auth cache and the texture atlas cache. Losing it costs a sign-in per
# unit and a 300 MB download per start, so it is a volume rather than
# container-local state. A new named volume copies this directory's owner, so
# without the chown the nonroot user could not write to it.
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/crew"]
