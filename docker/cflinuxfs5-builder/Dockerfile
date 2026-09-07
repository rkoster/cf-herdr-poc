# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0
FROM ${BASE_IMAGE} AS cflinuxfs5-tools
ARG TARGETARCH
ARG BUN_URL
ARG BUN_SHA256
ARG HERDR_URL
ARG HERDR_SHA256
ARG CF_URL
ARG CF_SHA256
RUN set -eux; command -v curl; command -v sha256sum; command -v unzip; command -v tar; \
    test "$TARGETARCH" = amd64 || test "$TARGETARCH" = arm64 || { echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 2; }; \
    test -n "${BUN_URL:-}" || { echo 'BUN_URL is required for the selected architecture' >&2; exit 2; }; \
    test -n "${BUN_SHA256:-}" || { echo 'BUN_SHA256 is required' >&2; exit 2; }; \
    test -n "${HERDR_URL:-}" || { echo 'HERDR_URL is required; official Herdr v0.8.2 asset is unresolved' >&2; exit 2; }; \
    test -n "${HERDR_SHA256:-}" || { echo 'HERDR_SHA256 is required' >&2; exit 2; }; \
    test -n "${CF_URL:-}" || { echo 'CF_URL is required; local CF version metadata is unresolved' >&2; exit 2; }; \
    test -n "${CF_SHA256:-}" || { echo 'CF_SHA256 is required' >&2; exit 2; }; \
    mkdir -p /tools/downloads /tools/bin; \
    curl --fail --location --retry 3 --output /tools/downloads/bun.zip "$BUN_URL"; \
    printf '%s  %s\n' "$BUN_SHA256" /tools/downloads/bun.zip | sha256sum -c -; \
    unzip -q /tools/downloads/bun.zip -d /tools/bun; \
    find /tools/bun -type f -name bun -exec install -m 0755 {} /tools/bin/bun \;; \
    test -x /tools/bin/bun; \
    curl --fail --location --retry 3 --output /tools/downloads/herdr "$HERDR_URL"; \
    printf '%s  %s\n' "$HERDR_SHA256" /tools/downloads/herdr | sha256sum -c -; \
    install -m 0755 /tools/downloads/herdr /tools/bin/herdr; \
    curl --fail --location --retry 3 --output /tools/downloads/cf "$CF_URL"; \
    printf '%s  %s\n' "$CF_SHA256" /tools/downloads/cf | sha256sum -c -; \
    case "$CF_URL" in *.tar.gz|*.tgz) mkdir -p /tools/cf && tar -xzf /tools/downloads/cf -C /tools/cf;; *.zip) mkdir -p /tools/cf && unzip -q /tools/downloads/cf -d /tools/cf;; *) echo 'CF_URL must be a .tgz, .tar.gz, or .zip archive' >&2; exit 2;; esac; \
    find /tools/cf -type f -name cf -exec install -m 0755 {} /tools/bin/cf \;; test -x /tools/bin/cf

FROM cflinuxfs5-tools AS build
ARG GOOS=linux
ARG GOARCH=amd64
COPY . /src
COPY --from=cflinuxfs5-tools /tools /tools
WORKDIR /src
RUN set -eux; command -v go || { echo 'cflinuxfs5 image lacks Go; provide a builder image with Go or add a pinned package source' >&2; exit 2; }; \
    command -v node || { echo 'cflinuxfs5 image lacks Node.js; Collie build requires a pinned Node/Bun toolchain' >&2; exit 2; }; \
    test -d collie/.git || { echo 'nested Collie checkout is required in the build context' >&2; exit 2; }; \
    /tools/bin/bun install --cwd web --frozen-lockfile; /tools/bin/bun run --cwd web build; \
    BUN_RUNTIME_BIN=/tools/bin/bun HERDR_RUNTIME_BIN=/tools/bin/herdr CF_BIN=/tools/bin/cf GOOS="$GOOS" GOARCH="$GOARCH" \
      RUNTIME_DIR=/work/dist/sandbox/runtime MANAGER_RUNTIME_DIR=/work/dist/manager-runtime \
      TARGET_INSTALL_DIR=/home/vcap/app/.sandbox/bin MANAGER_TARGET_INSTALL_DIR=/home/vcap/app/manager-runtime/bin \
      bash scripts/build-runtime.sh; \
    mkdir -p /work/dist/web; cp -R web/dist/. /work/dist/web/; \
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o /work/dist/manager ./cmd/manager; \
    test -x /work/dist/manager; \
    for f in manager-runtime/bin/bun manager-runtime/bin/cf manager-runtime/bin/collie sandbox/runtime/bin/bun sandbox/runtime/bin/herdr sandbox/runtime/bin/collie sandbox/runtime/bin/sandbox-bootstrap sandbox/start.sh; do test -x "/work/dist/$f"; done; \
    rm -rf /work/dist/sandbox/runtime/collie/.git /work/dist/sandbox/runtime/collie/web/node_modules

FROM scratch AS output
COPY --from=build /work/dist/ /
