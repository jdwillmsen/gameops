# Debian rather than Alpine, and digest-pinned to match the platform's rule
# that no deployed image tracks a mutable tag.
#
# bedrock-protocol depends on raknet-native, whose install hook shells out to
# cmake-js and compiles a C++ addon. musl prebuilds are unreliable, so an
# Alpine base either fails the build outright or silently falls back to the
# slower pure-JS RakNet path.
FROM node:24-bookworm-slim@sha256:3638d9a6fe4030bd716be989438248074489337ba3275657f93595428be4fc03 AS builder

# cmake is not in the base image and the addon cannot build without it. These
# stay in the builder stage: the runtime image has no business carrying a
# compiler.
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential cmake python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Dependencies before source so an edit to src/ does not invalidate the layer
# that compiles the native addon — by far the slowest step here.
COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build

# Drop devDependencies once the TypeScript is compiled. --ignore-scripts so the
# already-built native addon is left alone rather than reconfigured.
RUN npm prune --omit=dev --ignore-scripts

# The protocol version is fixed at build time because the data for every other
# version is stripped below. Rebuild to change it; the chart pins the image
# tag, so the two move together the same way the server version does.
ARG MC_VERSION=1.26.40

COPY scripts ./scripts
# Assert the pruned tree still loads. Without this the prune fails at runtime
# on a missing inherited directory rather than here, and only once the bot
# tries to connect.
RUN node scripts/prune-minecraft-data.mjs "$MC_VERSION" \
    && node -e "const d=require('minecraft-data')('bedrock_$MC_VERSION'); if(!d||!d.blocks||!d.items) { console.error('minecraft-data failed to load after prune'); process.exit(1) } console.log('post-prune load ok, protocol', d.version.version)" \
    && rm -rf ./scripts

# Two upstream packaging accidents, removed rather than shipped:
#
# jsp-raknet declares the TypeScript compiler as a runtime dependency, so
# `npm prune --omit=dev` keeps it — 65 MB of compiler inside a RakNet library
# that never requires it from its published JS.
#
# raknet-native leaves its whole cmake tree behind after compiling. Only
# build/Release holds the addon that `bindings` resolves; the rest is object
# files and the vendored C++ sources they came from.
RUN rm -rf node_modules/jsp-raknet/node_modules/typescript \
    && find node_modules/raknet-native/build -mindepth 1 -maxdepth 1 \
        ! -name Release -exec rm -rf {} + \
    && node -e "require('bedrock-protocol'); console.log('bedrock-protocol still loads after trim')"

FROM node:24-bookworm-slim@sha256:3638d9a6fe4030bd716be989438248074489337ba3275657f93595428be4fc03

ARG MC_VERSION=1.26.40
# Baked so the running default cannot drift from the version whose data
# survived the prune above.
ENV NODE_ENV=production \
    MC_VERSION=${MC_VERSION}

WORKDIR /app
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist
COPY package.json ./

# The token cache lives on a volume mounted here. Owned by the runtime user so
# a first start can create the profile without the pod needing fsGroup to line
# up by luck.
RUN mkdir -p /data/auth && chown -R node:node /data
VOLUME ["/data"]

USER node

# No healthcheck: the process holds a UDP session with no local endpoint to
# probe, and a bot between reconnect attempts is working correctly. Liveness is
# read from its structured stdout instead.
CMD ["node", "dist/index.js"]
