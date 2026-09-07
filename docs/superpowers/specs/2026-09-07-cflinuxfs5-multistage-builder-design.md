# cflinuxfs5 Multi-Stage Runtime Builder

## Goal

Replace the fragile Nix ELF relocation path used by live deployment with a reproducible Docker
builder based on the same cflinuxfs5 userspace family as the target Cloud Foundry stack.

## Build Stages

The builder uses multiple stages:

1. **Tools stage**: starts from the pinned cflinuxfs5 image, downloads pinned Bun, Herdr, and CF CLI
   artifacts, verifies SHA-256 checksums, and installs only build tools needed to compile Go and
   Collie assets.
2. **Build stage**: copies the repository and verified tools, builds the Go manager/bootstrap,
   builds the manager frontend, builds the nested Collie runtime, and assembles the sandbox and
   manager runtime trees.
3. **Output stage**: starts again from cflinuxfs5 and copies only `dist/` plus required runtime
   assets. It contains no package manager caches, source checkout, Git metadata, test files, or
   downloaded archives.

The output stage is used as a local artifact source. Cloud Foundry still deploys `dist/` with the
binary buildpack; the runtime image is a build compatibility and validation boundary, not a Docker
deployment target.

## Inputs And Integrity

Artifact URLs, versions, and SHA-256 checksums are explicit build arguments or a checked-in manifest.
The Docker build fails on checksum mismatch, missing artifacts, unsupported architecture, or a
runtime binary that cannot execute inside cflinuxfs5. No floating `latest` URLs are allowed.

The initial POC may use the Herdr release asset URL once its exact Linux asset name and checksum are
confirmed. It must not guess an asset or silently fall back to a host/Nix binary.

## Devbox Integration

`devbox run deploy` invokes the container builder, exports the resulting `dist/`, and then runs the
existing CF route/environment/deploy sequence. The deployment command does not use the Nix
relocation flag or host runtime paths. Docker is validated up front with an actionable error.

The old relocation scripts remain available temporarily for comparison and local experiments but are
not part of the live deployment path.

## Verification

The builder tests verify:

- Multi-stage Dockerfile structure and pinned base image.
- Checksum verification before artifacts enter the build stage.
- No source, archive, cache, test, or `.git` files in the output artifact.
- `bun --version`, `herdr --version`, and `cf version` execute inside the cflinuxfs5 build stage.
- Go, frontend, Collie, and shell tests pass using the generated artifact.
- The generated manager and sandbox executables retain their expected paths and CF runtime behavior.

Live verification then runs `devbox run deploy`, checks manager health/authentication, and executes
the fail-closed identity-aware smoke harness. Any remaining cflinuxfs5, route-policy, mTLS, or Pack
friction is recorded as observed evidence.

## Non-Goals

- Publishing a production Docker image.
- Removing the legacy relocation implementation immediately.
- Automatically selecting unpinned release versions.
- Hiding artifact download or build failures.
