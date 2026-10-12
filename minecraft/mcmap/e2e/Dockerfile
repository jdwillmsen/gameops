# The browser checks and the browser they drive. Microsoft's Playwright image
# carries the Chromium build that this version of playwright-core speaks to,
# with the libraries it needs, so the two cannot drift: the tag names the
# version in package.json and the digest is what actually pins it. Moving one
# means moving both.
FROM mcr.microsoft.com/playwright:v1.64.0-noble@sha256:06a9939e57531807f8d5fd76ce44b53165ffb7d7501d87ab10e285c20b1e971f
WORKDIR /opt/mcmap-e2e

# Built from the repo root, like every component's image.
COPY minecraft/mcmap/e2e/package.json minecraft/mcmap/e2e/package-lock.json ./
RUN npm ci --omit=dev --no-fund --no-audit && npm cache clean --force

COPY minecraft/mcmap/e2e/run.mjs ./
COPY minecraft/mcmap/e2e/lib ./lib
COPY minecraft/mcmap/e2e/checks ./checks

ARG VERSION=0.0.0
# HOME is where Chromium keeps a profile for the length of a run. The root
# filesystem is read-only where this runs, and /tmp is the volume it is given.
ENV MCMAP_E2E_VERSION=${VERSION} \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    HOME=/tmp \
    NODE_ENV=production

# Said outright: this runs once and ends, and its exit status is its health.
HEALTHCHECK NONE

# By number: the base image's pwuser, and a number is what a cluster that
# insists on a non-root user can check without reading the image.
USER 1000:1000
ENTRYPOINT ["node", "/opt/mcmap-e2e/run.mjs"]
