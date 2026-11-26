# syntax=docker/dockerfile:1
# The `browsers` image layer: Playwright browsers and their system libraries on
# top of the base job image (runners R5.44, R5.50; caches R5.41).
#   docker build --build-arg BASE_IMAGE=crsp-linux-base:ci \
#     --build-arg PLAYWRIGHT_VERSION=1.65.1 -t crsp-linux-browsers images/layers/browsers
#
# The Playwright version is pinned. A rebuild never lowers it for a pool
# (internal/runners/layers.go, NeverLower); the pool passes the version in.
ARG BASE_IMAGE=crsp-linux-base:ci
FROM ${BASE_IMAGE}

ARG PLAYWRIGHT_VERSION=1.65.1
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/crsp/browsers \
    PLAYWRIGHT_SKIP_BROWSER_GARBAGE_COLLECTION=1

USER root
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Install the pinned playwright-core in a throwaway folder, let it fetch the
# three browsers and the system packages they need, then drop the folder.
RUN set -eu; \
    mkdir -p /opt/crsp/pw "${PLAYWRIGHT_BROWSERS_PATH}"; \
    cd /opt/crsp/pw; \
    npm init -y >/dev/null; \
    npm install --no-audit --no-fund --save-exact "playwright-core@${PLAYWRIGHT_VERSION}"; \
    export DEBUG=pw:install; \
    # CRSP325: security.ubuntu.com has served index entries whose .deb files answer 404 for hours
    # (libasound2). archive.ubuntu.com carries the same security pocket, so use it, and try three times.
    for f in /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list; do \n      [ -f "$f" ] && sed -i 's#http://security.ubuntu.com/ubuntu#http://archive.ubuntu.com/ubuntu#g' "$f"; \n    done; \n    for i in 1 2 3; do \n      timeout 900 node node_modules/playwright-core/cli.js install-deps chromium firefox webkit && break; \n      [ "$i" = 3 ] && exit 1; \n      echo "install-deps failed (attempt $i), retrying in 30 s"; sleep 30; \n    done; \n    for b in chromium firefox webkit; do \
      timeout 600 node node_modules/playwright-core/cli.js install "$b" </dev/null; \
    done; \
    echo "${PLAYWRIGHT_VERSION}" > /opt/crsp/browsers/.crsp-playwright-version; \
    rm -rf /var/lib/apt/lists/* /root/.npm; \
    chmod -R a+rX /opt/crsp

# Self-check: launch each browser headless once, or the build fails.
RUN cd /opt/crsp/pw && node -e ' \
  const pw = require("playwright-core"); \
  (async () => { \
    for (const t of [pw.chromium, pw.firefox, pw.webkit]) { \
      const b = await t.launch({ headless: true, timeout: 60000 }); \
      const p = await b.newPage(); \
      await p.setContent("<p>ok</p>"); \
      await b.close(); \
    } \
  })().catch((e) => { console.error(e); process.exit(1); })'

USER runner
WORKDIR /home/runner
