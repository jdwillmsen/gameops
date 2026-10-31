# Built from the repo root so the pack sources under packs/ are in context.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS stage
COPY packs/ /src/
# Iterates the folders, so a new theme needs no edit here. lab-mods-stage0 is
# still delivered by the chart's ConfigMap.
RUN set -eu; \
    for d in /src/*/; do \
      t=$(basename "$d"); \
      [ "$t" != lab-mods-stage0 ] || continue; \
      for k in behavior_pack resource_pack; do \
        [ ! -d "$d$k" ] || { mkdir -p "/out/$t"; cp -R "$d$k" "/out/$t/$k"; }; \
      done; \
    done; \
    mkdir -p /out; \
    find /out \( -name '*.test.js' -o -name policy.txt -o -iname 'README*' \) -type f -delete; \
    find /out -name assets-src -type d -prune -exec rm -rf {} +

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache jq
COPY --from=stage /out/ /packs/
COPY minecraft/lab-packs/install.sh /install.sh

# Explicit non-root user equal to the lab pod's own (uid 1000, gid 3000), so the
# default matches the world owner and a scanner sees a non-root image. The
# pod's securityContext still decides at runtime.
USER 1000:3000

# Run-to-completion installer: there is nothing to probe.
HEALTHCHECK NONE
ENTRYPOINT ["/install.sh"]
