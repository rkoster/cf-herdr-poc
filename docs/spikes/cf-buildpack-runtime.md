# CF Buildpack Runtime Spike

Status: Deferred for production; live lab evidence recorded

Original deferred date: 2026-09-04

Observed date: 2026-09-08

The live lab used `ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0`. It is based on Ubuntu 24.04 with glibc 2.39 and initially contains none of Go, Node, Bun, Herdr, or the CF CLI. A multi-stage builder downloaded the pinned Bun 1.3.13, Herdr 0.8.2, and CF CLI 8.19.0 artifacts, added the Go toolchain, and produced the cflinux-compatible distribution. The Docker build context and artifact pipeline completed, with the builder emitting a scratch artifact.

The deployed package was approximately 214.7 MiB. The manager's 1 GB quota was initially exhausted by the sandbox overlay; 2 GB was required. A 4 GB quota was denied by the lab quota. `devbox run deploy` initially needed an explicit profile for CF and Herdr discovery and was subsequently fixed. Manager CF authentication required `CF_API`, `CF_USERNAME`, `CF_PASSWORD`, `CF_ORG`, and `CF_SPACE`; credential values are intentionally not recorded here.

After Herdr supervision and bundled CF authentication were in place, the manager reached `1/1` healthy. This does not establish a complete successful lifecycle smoke test; the remaining live result is recorded in `docs/smoke-test.md`.

The relocation mode is a lab-only workaround. Direct execution with the cflinuxfs loader and bundled Nix libc failed with undefined `__tunable_is_initialized@GLIBC_PRIVATE`; explicit execution through the bundled Nix loader succeeded. `ALLOW_NIX_RUNTIME_RELOCATION=1` therefore patches each direct ELF to an absolute private-loader interpreter and an origin-relative RPATH. Sandbox binaries are bound to `/home/vcap/app/.sandbox/bin`; independent manager Bun and Collie copies are bound to `/home/vcap/app/manager-runtime/bin`, while manager Collie reuses the sandbox Collie asset tree. Relocation recursively bundles each startup `DT_NEEDED` graph and available glibc NSS/DNS resolver modules. Exact install-path binding and duplicated private executable libraries increase build and upload friction and are not the production recommendation; production should use official static or portable artifacts.

The bundle is not proven to be a complete dynamic closure. Unobserved `dlopen` choices and absolute runtime asset paths can still escape the startup graph. Packaged ELF loader metadata is checked for actionable `/nix/store/` paths, but arbitrary embedded diagnostics strings are not rejected. The live cflinuxfs smoke test exercised deployment and several lifecycle prerequisites, but did not complete successfully.

The manager CF CLI is deliberately packaged differently from Bun, Herdr, and Collie. `manager-runtime/bin/cf` is an executable wrapper around `cf.real`; it invokes the private `.cf-libs` loader with an explicit `--library-path`, forwards all arguments, and preserves the payload exit status through `exec`. CF CLI does not require `process.execPath` or self-spawn identity, so this wrapper avoids the relocated CF CLI's direct-ELF smoke failure while keeping the manager provider's configured executable path unchanged. The other runtimes retain direct ELF relocation because their process identity and self-spawn behavior must remain intact.

## Commands

Run only after selecting a disposable CF space. The live lab used the pinned cflinuxfs5 builder above. To evaluate the lab workaround instead, resolve the already-installed Nix runtimes, export `ALLOW_NIX_RUNTIME_RELOCATION=1`, and do not download replacements:

```bash
export BUN_RUNTIME_BIN=/absolute/path/to/portable-linux-bun
export HERDR_RUNTIME_BIN=/absolute/path/to/portable-linux-herdr
# Lab alternative only:
# export BUN_RUNTIME_BIN="$(command -v bun)"
# export HERDR_RUNTIME_BIN="$(command -v herdr)"
# export ALLOW_NIX_RUNTIME_RELOCATION=1
time GOOS=linux GOARCH=amd64 bash scripts/build.sh
du -sh dist dist/sandbox/runtime
file dist/manager dist/manager-runtime/bin/* dist/sandbox/runtime/bin/*
ldd dist/manager dist/sandbox/runtime/bin/sandbox-bootstrap
rm -rf dist/runtime-spike
mkdir -p dist/runtime-spike
cp -R dist/sandbox/runtime dist/runtime-spike/.sandbox
cf version
cf buildpacks
cf push cf-herdr-runtime-spike -p dist/runtime-spike --no-route -b binary_buildpack -c './.sandbox/start.sh' --no-start
cf app cf-herdr-runtime-spike --guid
cf start cf-herdr-runtime-spike
cf logs cf-herdr-runtime-spike --recent
cf ssh cf-herdr-runtime-spike -c 'pwd; test -x app/.sandbox/start.sh; test -x app/.sandbox/bin/bun; test -x app/.sandbox/bin/herdr; app/.sandbox/bin/bun --version; app/.sandbox/bin/herdr --version; touch /home/vcap/data/write-test; test -f /home/vcap/data/write-test'
cf delete cf-herdr-runtime-spike -f
rm -rf dist/runtime-spike
```

## Assertions

- Staging preserves executable bits for manager, bootstrap, Bun, Herdr, and Collie.
- Manager and bootstrap have no dynamic dependencies. Portable runtime binaries have no unresolved or `/nix/store` dependencies; on amd64 the lab-relocated sandbox Bun interpreter is exactly `/home/vcap/app/.sandbox/bin/.bun-libs/ld-linux-x86-64.so.2`, with corresponding executable-specific bundled Nix loaders and origin-relative private library paths for the other relocated binaries.
- The binary buildpack starts the exact configured command without downloading artifacts.
- Bundled Bun and Herdr execute in the Diego cell.
- Application bits are readable and `/home/vcap/data` is writable.
- Cleanup removes the temporary app and route count does not increase.

## Capture

Record CF CLI/API version, Diego stack, binary buildpack name/version, source and uploaded byte sizes, upload/staging/start durations, staged command, process exit status, writable paths, executable-bit result, `file`/`ldd` summaries, Bun/Herdr versions, log timestamps, cleanup duration, and any workaround. Do not record credentials, tokens, certificates, or fabricated observations.
