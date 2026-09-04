# CF Buildpack Runtime Spike

Status: Deferred

Date: 2026-09-04

Reason: Portable Linux Bun and Herdr binaries are not available locally, so the packaged runtime cannot be pushed or executed without violating the repository's no-download policy.

## Commands

Run only after independently supplying portable binaries and selecting a disposable CF space:

```bash
export BUN_RUNTIME_BIN=/absolute/path/to/portable-linux-bun
export HERDR_RUNTIME_BIN=/absolute/path/to/portable-linux-herdr
time GOOS=linux GOARCH=amd64 bash scripts/build.sh
du -sh dist dist/sandbox/runtime
file dist/manager dist/sandbox/runtime/bin/*
ldd dist/manager dist/sandbox/runtime/bin/sandbox-bootstrap
cf version
cf buildpacks
cf push cf-herdr-runtime-spike -p dist --no-route -b binary_buildpack -c './manager' --no-start
cf app cf-herdr-runtime-spike --guid
cf start cf-herdr-runtime-spike
cf logs cf-herdr-runtime-spike --recent
cf ssh cf-herdr-runtime-spike -c 'pwd; test -x app/manager; test -x app/sandbox/runtime/bin/bun; test -x app/sandbox/runtime/bin/herdr; app/sandbox/runtime/bin/bun --version; app/sandbox/runtime/bin/herdr --version; touch /home/vcap/data/write-test; test -f /home/vcap/data/write-test'
cf delete cf-herdr-runtime-spike -f
```

## Assertions

- Staging preserves executable bits for manager, bootstrap, Bun, Herdr, and Collie.
- Manager and bootstrap have no dynamic dependencies; portable runtime binaries have no unresolved or `/nix/store` dependencies.
- The binary buildpack starts the exact configured command without downloading artifacts.
- Bundled Bun and Herdr execute in the Diego cell.
- Application bits are readable and `/home/vcap/data` is writable.
- Cleanup removes the temporary app and route count does not increase.

## Capture

Record CF CLI/API version, Diego stack, binary buildpack name/version, source and uploaded byte sizes, upload/staging/start durations, staged command, process exit status, writable paths, executable-bit result, `file`/`ldd` summaries, Bun/Herdr versions, log timestamps, cleanup duration, and any workaround. Do not record credentials, tokens, certificates, or fabricated observations.
