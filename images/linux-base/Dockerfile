# syntax=docker/dockerfile:1
# Linux base job image, like a GitHub-hosted runner (runners R5.49, R5.48).
# Build context: this folder, with the linux build of crsp copied in as ./crsp
# (CI does it; see the image job of .github/workflows/ci.yml).
#   docker build --build-arg TARGETARCH=amd64 -t crsp-linux-base images/linux-base
#
# Every download below is pinned and SHA-256 checked; a wrong hash fails the build.

# CRSP323: the last stage builds on this. The default is the stage below, so a
# plain `docker build` is unchanged; CI passes a stored tools image when the
# inputs match one that main built (scripts/ci/image_key.py).
ARG BASE_IMAGE=tools

# Ubuntu 24.04 LTS, pinned by digest (the multi-arch index).
FROM ubuntu:24.04@sha256:2198e4c65076e9cb0b3d695219df667c0e3b0072da64ca3c2648aca17dcecaf5 AS tools

ARG TARGETARCH=amd64
ARG NODE_VERSION=26.13.1
ARG NODE_SHA256_AMD64=5c063e46bab0fbc1c17cc445d7ff129cbdcd0471be6ed83b6ab864f8143bc864
ARG NODE_SHA256_ARM64=89e1853138db51e8b82d3e98ba9c0d3f2956a28e5e55ea1b7e12d25aae92d5dd
ARG COREPACK_VERSION=0.36.0
ARG COREPACK_SHA256=9128cfe26aee0c99f4fc68c15c6b8b12309a68be0de2c012cdfea2a463cb722f
ARG GH_VERSION=2.103.3
ARG GH_SHA256_AMD64=4c806978ffce1cec70853302a56d70ac6da4f19bae7adca4e1a8ab00633dd91d
ARG GH_SHA256_ARM64=ee32df2323c8847f5fac6cb6b4b69ca75828e9329a8f22f677021f7b3306ce63
# The Actions runner: the same version and hashes as `pins` in
# internal/runners/version.go (a test keeps them equal), so the agent's update
# check and the image key read the same pin (runners R5.37, R5.49).
ARG RUNNER_VERSION=2.338.2
ARG RUNNER_SHA256_AMD64=baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40
ARG RUNNER_SHA256_ARM64=670adb2f268fe21dcbb58b3db0e0aa9d5b3bddfe2c651481336e561ff86de945
ARG JOB_UID=1001

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8
# Node's bin comes first on PATH for every user and before the corepack step.
ENV PATH=/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# apt retries and timeouts: a slow or flaky mirror retries instead of hanging
# for the whole job (OPS41). Layers built on this image inherit the file.
RUN printf '%s\n' 'Acquire::Retries "5";' 'Acquire::http::Timeout "30";' 'Acquire::https::Timeout "30";' \
      > /etc/apt/apt.conf.d/80-crsp-retries

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl git jq libatomic1 libicu74 \
      pkg-config postgresql-client python3 unzip xz-utils zip \
 && rm -rf /var/lib/apt/lists/*

# Node LTS (no corepack inside the tarball since Node 25, so corepack is
# installed from a pinned, hash-checked npm tarball) and the pnpm/yarn shims.
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) node_arch=x64;   node_sha="${NODE_SHA256_AMD64}" ;; \
      arm64) node_arch=arm64; node_sha="${NODE_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    f="node-v${NODE_VERSION}-linux-${node_arch}.tar.xz"; \
    curl -fsSL -o "/tmp/${f}" "https://nodejs.org/dist/v${NODE_VERSION}/${f}"; \
    echo "${node_sha}  /tmp/${f}" | sha256sum -c -; \
    tar -xJf "/tmp/${f}" -C /usr/local --strip-components=1 --no-same-owner \
      --exclude='*/CHANGELOG.md' --exclude='*/README.md' --exclude='*/LICENSE'; \
    rm -f "/tmp/${f}"; \
    curl -fsSL -o /tmp/corepack.tgz "https://registry.npmjs.org/corepack/-/corepack-${COREPACK_VERSION}.tgz"; \
    echo "${COREPACK_SHA256}  /tmp/corepack.tgz" | sha256sum -c -; \
    npm install -g /tmp/corepack.tgz; \
    rm -f /tmp/corepack.tgz; \
    corepack enable

# GitHub CLI.
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) gh_sha="${GH_SHA256_AMD64}" ;; \
      arm64) gh_sha="${GH_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    f="gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSL -o "/tmp/${f}" "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${f}"; \
    echo "${gh_sha}  /tmp/${f}" | sha256sum -c -; \
    tar -xzf "/tmp/${f}" -C /tmp; \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh" /usr/local/bin/gh; \
    rm -rf "/tmp/${f}" "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}"

# A fixed non-root job user with no sudo.
RUN groupadd --gid "${JOB_UID}" runner \
 && useradd --uid "${JOB_UID}" --gid runner --create-home --shell /bin/bash runner \
 && ! command -v sudo

# The Actions runner in /opt/actions-runner, owned by the job user so a job can
# copy it (runners R5.25, R5.49). Its own updates are off: the agent updates
# images (R5.38). A JIT config registers it at run time; nothing is registered here.
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) runner_arch=x64;   runner_sha="${RUNNER_SHA256_AMD64}" ;; \
      arm64) runner_arch=arm64; runner_sha="${RUNNER_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    f="actions-runner-linux-${runner_arch}-${RUNNER_VERSION}.tar.gz"; \
    curl -fsSL -o "/tmp/${f}" "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${f}"; \
    echo "${runner_sha}  /tmp/${f}" | sha256sum -c -; \
    mkdir -p /opt/actions-runner; \
    tar -xzf "/tmp/${f}" -C /opt/actions-runner --no-same-owner; \
    rm -f "/tmp/${f}"; \
    apt-get update; \
    /opt/actions-runner/bin/installdependencies.sh; \
    rm -rf /var/lib/apt/lists/*; \
    printf 'DISABLE_RUNNER_UPDATE=1\n' > /opt/actions-runner/.env; \
    chown -R "${JOB_UID}:${JOB_UID}" /opt/actions-runner

# Self-check: every tool runs, or the build fails (runners R5.49).
RUN git --version && curl --version >/dev/null && jq --version && zip -v >/dev/null \
 && unzip -v >/dev/null && python3 --version && gcc --version >/dev/null && make --version >/dev/null \
 && pkg-config --version && psql --version \
 && node --version && corepack --version && gh --version \
 && test -x "$(command -v pnpm)" && test -x "$(command -v yarn)" \
 && ldconfig -p | grep libatomic >/dev/null

USER runner
# The runner starts as the job user and prints the pinned version.
RUN /opt/actions-runner/run.sh --version | grep -x "${RUNNER_VERSION}" >/dev/null \
 && test -f /opt/actions-runner/.env
WORKDIR /home/runner

# The last stage adds only the crsp binary, which changes on every commit. The
# stage above holds every slow layer and does not depend on crsp, so CI builds
# it with `--target tools`, caches it, and builds the tool layers on it (OPS41).
# A plain `docker build` builds this last stage: the full base image.
FROM ${BASE_IMAGE}
# The crsp binary, for `crsp vm` and `crsp db-env` (runners R5.48). A running
# container job gets the agent's own binary mounted over it.
COPY --chmod=0755 crsp /usr/local/bin/crsp
RUN crsp version >/dev/null
