# The browser checks and the browser they drive. Microsoft's Playwright image
# carries the Chromium build that this version of playwright-core speaks to,
# with the libraries it needs, so the two cannot drift: the tag names the
# version in package.json and the digest is what actually pins it. Moving one
# means moving both.
FROM mcr.microsoft.com/playwright:v1.64.1-noble@sha256:7852dc5a7d72f5fe458a597d690f6c42fbc8e9e26ddfa95f8e6ef552558d9bbf
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

# The base image's own unprivileged user, pwuser, which is uid 1001 there.
# uid 1000 in it is `ubuntu`, the distribution's default login, and is not
# the user the image means a browser to run as. Named by number, since a
# cluster that insists on a non-root user can check a number without
# reading the image; the build fails if the name and the number ever part.
RUN test "$(id -u pwuser):$(id -g pwuser)" = "1001:1001"
USER 1001:1001
ENTRYPOINT ["node", "/opt/mcmap-e2e/run.mjs"]
