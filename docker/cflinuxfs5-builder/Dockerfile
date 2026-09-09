# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0
FROM ${BASE_IMAGE} AS cflinuxfs5-tools
ARG TARGETARCH
ARG BUN_URL
ARG BUN_SHA256
ARG GO_URL
ARG GO_SHA256
ARG HERDR_URL
ARG HERDR_SHA256
ARG OPENCODE_URL
ARG OPENCODE_SHA256
ARG CF_URL
ARG CF_SHA256
COPY docker/cflinuxfs5-builder/extract-cf-cli.sh /usr/local/bin/extract-cf-cli.sh
RUN set -eux; command -v curl; command -v sha256sum; command -v unzip; command -v tar; \
    test "$TARGETARCH" = amd64 || test "$TARGETARCH" = arm64 || { echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 2; }; \
    test -n "${BUN_URL:-}" || { echo 'BUN_URL is required for the selected architecture' >&2; exit 2; }; \
     test -n "${BUN_SHA256:-}" || { echo 'BUN_SHA256 is required' >&2; exit 2; }; \
     test -n "${GO_URL:-}" || { echo 'GO_URL is required for the selected architecture' >&2; exit 2; }; \
     test -n "${GO_SHA256:-}" || { echo 'GO_SHA256 is required' >&2; exit 2; }; \
     test -n "${HERDR_URL:-}" || { echo 'HERDR_URL is required; use the verified Herdr v0.8.2 release asset' >&2; exit 2; }; \
     test -n "${HERDR_SHA256:-}" || { echo 'HERDR_SHA256 is required' >&2; exit 2; }; \
     test -n "${OPENCODE_URL:-}" || { echo 'OPENCODE_URL is required; use a verified OpenCode release asset' >&2; exit 2; }; \
     test -n "${OPENCODE_SHA256:-}" || { echo 'OPENCODE_SHA256 is required' >&2; exit 2; }; \
     test -n "${CF_URL:-}" || { echo 'CF_URL is required; local CF version metadata is unresolved' >&2; exit 2; }; \
     test -n "${CF_SHA256:-}" || { echo 'CF_SHA256 is required' >&2; exit 2; }; \
     case "$CF_URL" in *.tar.gz|*.tgz) cf_format=tgz ;; *.zip) cf_format=zip ;; *) echo 'CF_URL must end in .tgz, .tar.gz, or .zip' >&2; exit 2 ;; esac; \
     mkdir -p /tools/downloads /tools/bin; \
     curl --fail --location --retry 3 --output /tools/downloads/bun.zip "$BUN_URL"; \
    printf '%s  %s\n' "$BUN_SHA256" /tools/downloads/bun.zip | sha256sum -c -; \
    unzip -q /tools/downloads/bun.zip -d /tools/bun; \
    find /tools/bun -type f -name bun -exec install -m 0755 {} /tools/bin/bun \;; \
      test -x /tools/bin/bun; \
      ln -s /tools/bin/bun /tools/bin/bunx; \
      test -x /tools/bin/bunx; \
      ln -s /tools/bin/bun /usr/local/bin/bunx; \
     test -x /usr/local/bin/bunx; \
     /usr/local/bin/bunx --version; \
     curl --fail --location --retry 3 --output /tools/downloads/go.tar.gz "$GO_URL"; \
     printf '%s  %s\n' "$GO_SHA256" /tools/downloads/go.tar.gz | sha256sum -c -; \
     rm -rf /usr/local/go; tar -C /usr/local -xzf /tools/downloads/go.tar.gz; \
     test -x /usr/local/go/bin/go; \
      curl --fail --location --retry 3 --output /tools/downloads/herdr "$HERDR_URL"; \
     printf '%s  %s\n' "$HERDR_SHA256" /tools/downloads/herdr | sha256sum -c -; \
      install -m 0755 /tools/downloads/herdr /tools/bin/herdr; \
     curl --fail --location --retry 3 --output /tools/downloads/opencode.tar.gz "$OPENCODE_URL"; \
     printf '%s  %s\n' "$OPENCODE_SHA256" /tools/downloads/opencode.tar.gz | sha256sum -c -; \
     mkdir -p /tools/opencode; tar -C /tools/opencode -xzf /tools/downloads/opencode.tar.gz; \
     opencode_count="$(find /tools/opencode -type f -name opencode -perm -u+x | wc -l)"; \
     test "$opencode_count" -eq 1 || { echo 'OpenCode archive must contain exactly one executable named opencode' >&2; exit 2; }; \
     find /tools/opencode -type f -name opencode -perm -u+x -exec install -m 0755 {} /tools/bin/opencode \;; \
     test -x /tools/bin/opencode; \
      curl --fail --location --retry 3 --output /tools/downloads/cf "$CF_URL"; \
     printf '%s  %s\n' "$CF_SHA256" /tools/downloads/cf | sha256sum -c -; \
     /usr/local/bin/extract-cf-cli.sh /tools/downloads/cf /tools/bin "$cf_format"

FROM cflinuxfs5-tools AS build
ARG GOOS=linux
ARG GOARCH=amd64
ARG DIRECT_SANDBOX=
ARG SANDBOX_TARGET_INSTALL_DIR=
ENV PATH=/usr/local/go/bin:/tools/bin:/usr/local/bin:$PATH
COPY . /src
COPY --from=cflinuxfs5-tools /tools /tools
WORKDIR /src
RUN set -eux; command -v go; \
    ln -sf /tools/bin/bun /usr/local/bin/bunx; command -v bunx; bunx --version; \
    /tools/bin/bun install --cwd web --frozen-lockfile; /tools/bin/bun run --cwd web build; \
	     sandbox_target=/home/vcap/app/sandbox-runtime/bin; \
       if test -n "${SANDBOX_TARGET_INSTALL_DIR:-}"; then sandbox_target="$SANDBOX_TARGET_INSTALL_DIR"; fi; \
     BUN_RUNTIME_BIN=/tools/bin/bun HERDR_RUNTIME_BIN=/tools/bin/herdr OPENCODE_RUNTIME_BIN=/tools/bin/opencode CF_BIN=/tools/bin/cf GOOS="$GOOS" GOARCH="$GOARCH" \
       RUNTIME_DIR=/work/dist/sandbox/runtime MANAGER_RUNTIME_DIR=/work/dist/manager-runtime \
        SANDBOX_TARGET_INSTALL_DIR="$sandbox_target" MANAGER_TARGET_INSTALL_DIR=/home/vcap/app/manager-runtime/bin \
      bash scripts/build-runtime.sh; \
    mkdir -p /work/dist/web; cp -R web/dist/. /work/dist/web/; \
     CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o /work/dist/manager ./cmd/manager; \
     test -x /work/dist/manager; \
        for f in manager-runtime/bin/bun manager-runtime/bin/herdr manager-runtime/bin/cf manager-runtime/bin/collie sandbox/runtime/bin/bun sandbox/runtime/bin/herdr sandbox/runtime/bin/collie sandbox/runtime/bin/opencode sandbox/runtime/bin/sandbox-bootstrap sandbox/runtime/start.sh; do test -x "/work/dist/$f" || { printf 'error: missing executable artifact: %s\n' "/work/dist/$f" >&2; exit 1; }; done; \
      rm -rf /work/dist/sandbox/runtime/collie/.git; \
      rm -rf /work/dist/sandbox/runtime/collie/web/node_modules

FROM scratch AS output
COPY --from=build /work/dist/ /
