# cflinuxfs5 Builder Spike

The runtime builder is pinned to `ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0` and emits only the
contents of `dist/` through Docker BuildKit's local exporter. `devbox run deploy` selects this path
by default; `BUILD_MODE=nix-relocation` is retained only as an explicit lab fallback.

Bun 1.3.13 is verified from the official release metadata:

- URL: `https://github.com/oven-sh/bun/releases/download/bun-v1.3.13/bun-linux-x64.zip`
- SHA-256: `79c0771fa8b92c33aae41e15a0e0d307ea99d0e2f00317c71c6c53237a78e25a`

Herdr `v0.8.2` is verified from the official GitHub release metadata:

- AMD64 URL: `https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-x86_64`
- AMD64 SHA-256: `976150a14d490c94b243ea2e1a7eb2dfb67f12e36b182db90936f6728e6aecf4`
- ARM64 URL: `https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-aarch64`
- ARM64 SHA-256: `f55610658e1c2e0d2aaef730b4b2ab885f7f8ba00285ab372bfb14f2e3d5b40d`

The local CF executable reports `0.0.0-unknown-version`, so it cannot establish an exact CF CLI
release artifact or checksum. CF CLI values remain required build arguments. The Dockerfile fails
before download if a URL or checksum is absent; no floating URL, host binary, or fabricated checksum
is accepted.

The first real build remains blocked until operators provide:

- `CF_URL` and `CF_SHA256` for an exact official CF CLI Linux release matching the target architecture.
- Confirmation that the pinned cflinuxfs5 image contains Go and Node.js/Bun support required by the
  existing Collie build. If absent, the tools stage must add pinned package inputs rather than use
  floating package-manager downloads.
