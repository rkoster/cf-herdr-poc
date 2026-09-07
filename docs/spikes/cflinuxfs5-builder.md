# cflinuxfs5 Builder Spike

The runtime builder is pinned to `ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0` and emits only the
contents of `dist/` through Docker BuildKit's local exporter. `devbox run deploy` selects this path
by default; `BUILD_MODE=nix-relocation` is retained only as an explicit lab fallback.

Bun 1.3.13 is verified from the official release metadata:

- URL: `https://github.com/oven-sh/bun/releases/download/bun-v1.3.13/bun-linux-x64.zip`
- SHA-256: `79c0771fa8b92c33aae41e15a0e0d307ea99d0e2f00317c71c6c53237a78e25a`

Herdr `v0.8.2` does not have a confirmed official Linux asset URL and SHA-256 in the release
metadata available for this spike. The local CF executable reports `0.0.0-unknown-version`, so it
also cannot establish an exact CF CLI release artifact or checksum. Both are therefore required
build arguments. The Dockerfile fails before download if either URL or checksum is absent; no
floating URL, host binary, or fabricated checksum is accepted.

The first real build remains blocked until operators provide:

- `HERDR_URL` and `HERDR_SHA256` for the official Herdr 0.8.2 Linux asset.
- `CF_URL` and `CF_SHA256` for an exact official CF CLI Linux release matching the target architecture.
- Confirmation that the pinned cflinuxfs5 image contains Go and Node.js/Bun support required by the
  existing Collie build. If absent, the tools stage must add pinned package inputs rather than use
  floating package-manager downloads.
