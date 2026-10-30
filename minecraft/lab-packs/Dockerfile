# Built from the repo root so the pack sources under packs/ are in context.
FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS stage
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

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
RUN apk add --no-cache jq
COPY --from=stage /out/ /packs/
COPY minecraft/lab-packs/install.sh /install.sh

# No USER on purpose: the pod's securityContext decides the user (uid 1000,
# gid 3000 in the lab), which must be able to write the world files. A fixed
# uid baked in here could not follow that.
ENTRYPOINT ["/install.sh"]
