#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT="$ROOT/scripts/smoke.sh"
SECRET='manager-token-must-not-leak'
TEST_DIR=

cleanup_test_dir() {
  [[ -z ${TEST_DIR:-} ]] || rm -rf "$TEST_DIR"
}
trap cleanup_test_dir EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  case "$1" in *"$2"*) ;; *) fail "expected output to contain: $2" ;; esac
}

assert_not_contains() {
  case "$1" in *"$2"*) fail "output leaked forbidden text: $2" ;; *) ;; esac
}

make_fakes() {
  cleanup_test_dir
  TEST_DIR=$(TMPDIR=/tmp mktemp -d)
  FAKE_BIN="$TEST_DIR/bin"
  LOG="$TEST_DIR/commands.log"
  STATE="$TEST_DIR/state"
  mkdir -p "$FAKE_BIN" "$STATE"

  cat >"$FAKE_BIN/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'curl' >>"$FAKE_LOG"
for arg in "$@"; do
  case "$arg" in *manager-token-must-not-leak*) exit 91 ;; esac
  printf ' <%s>' "$arg" >>"$FAKE_LOG"
done
printf '\n' >>"$FAKE_LOG"
url=${!#}
method=GET
output=
write_status=0
previous=
for arg in "$@"; do
  if [[ "$previous" == -X || "$previous" == --request ]]; then method=$arg; fi
  if [[ "$previous" == -o || "$previous" == --output ]]; then output=$arg; fi
  if [[ "$previous" == -w || "$previous" == --write-out ]]; then write_status=1; fi
  previous=$arg
done
body=''
status=200
case "$url $method" in
  */manager/api/session\ POST) status=204 ;;
  */manager/api/sandboxes\ POST)
    touch "$FAKE_STATE/created"
    body='{"name":"smoke-fixed","repository":"https://example.invalid/repo.git","buildpack":"binary_buildpack","desired":"present","phase":"creating","createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:00Z"}'
    status=202
    ;;
  */manager/api/sandboxes/smoke-fixed\ DELETE)
    touch "$FAKE_STATE/delete-requested"
    status=${FAKE_MANAGER_DELETE_STATUS:-202}
    [[ $status == 202 ]] && touch "$FAKE_STATE/cf-deleted"
    ;;
  */manager/api/sandboxes\ GET)
    count_file="$FAKE_STATE/polls"
    count=0
    [[ -f "$count_file" ]] && count=$(<"$count_file")
    count=$((count + 1)); printf '%s' "$count" >"$count_file"
    if [[ -f "$FAKE_STATE/delete-requested" && "${FAKE_MANAGER_DELETE_STATUS:-202}" == 202 ]]; then
      body='[]'
    elif (( count == 1 )); then
      body='[{"name":"smoke-fixed","repository":"https://example.invalid/repo.git","buildpack":"binary_buildpack","desired":"present","phase":"creating","createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:00Z"}]'
    else
      body='[{"name":"smoke-fixed","repository":"https://example.invalid/repo.git","buildpack":"binary_buildpack","desired":"present","phase":"ready","packMemberId":"smoke-fixed","operations":[{"name":"clone","duration":1000000000,"success":true},{"name":"stage","duration":2000000000,"success":true}],"createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:03Z"}]'
    fi
    ;;
  */collie/api/snapshot*\ GET)
    if [[ -f "$FAKE_STATE/delete-requested" ]]; then
      body='{"bridge":{"connected":true},"agents":[],"shellPanes":[],"workspaces":[],"tabs":[],"sessions":[],"servers":[{"id":"lead","name":"lead","isLead":true,"reachable":true,"protocol":"ok","lastSeenAt":1}],"ts":1}'
    else
      body='{"bridge":{"connected":true},"agents":[],"shellPanes":[],"workspaces":[],"tabs":[],"sessions":[],"servers":[{"id":"lead","name":"lead","isLead":true,"reachable":true,"protocol":"ok","lastSeenAt":1},{"id":"smoke-fixed","name":"smoke-fixed","isLead":false,"reachable":true,"protocol":"ok","lastSeenAt":1}],"ts":1}'
    fi
    ;;
  */collie/api/pack\ GET)
    if [[ -f "$FAKE_STATE/delete-requested" ]]; then
      body='{"pack":{"id":"pack","name":"pack","secretGeneration":1,"rotatedAt":1},"self":{"id":"lead","name":"lead","version":"1"},"deputy":null,"members":[],"ts":1}'
    else
      body='{"pack":{"id":"pack","name":"pack","secretGeneration":1,"rotatedAt":1},"self":{"id":"lead","name":"lead","version":"1"},"deputy":null,"members":[{"id":"smoke-fixed","name":"smoke-fixed","isLead":false,"health":"reachable","lastSeenAt":1,"secretBehind":false,"provisional":false}],"ts":1}'
    fi
    ;;
  https://smoke-fixed.identity.invalid/pack/v1/hello\ GET) status=404 ;;
  *) status=500; body='{"error":"unexpected fake curl request"}' ;;
esac
if [[ -n "$output" ]]; then printf '%s' "$body" >"$output"; else printf '%s' "$body"; fi
if (( write_status )); then printf '%s' "$status"; fi
case "$status" in 2??) exit 0 ;; *) exit 22 ;; esac
EOF

  cat >"$FAKE_BIN/cf" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'cf' >>"$FAKE_LOG"
for arg in "$@"; do printf ' <%s>' "$arg" >>"$FAKE_LOG"; done
printf '\n' >>"$FAKE_LOG"
if [[ ${1:-} == curl ]]; then
  case "${2:-}" in
    '/v3/apps?names=smoke-fixed')
      if [[ -f "$FAKE_STATE/cf-deleted" ]]; then printf '{"resources":[]}'
      else printf '{"resources":[{"guid":"sandbox-guid","name":"smoke-fixed"}]}'
      fi
      ;;
    /v3/apps/sandbox-guid/routes)
      printf '{"resources":[{"guid":"route-guid","host":"smoke-fixed","url":"smoke-fixed.identity.invalid","relationships":{"domain":{"data":{"guid":"domain-guid"}}}}]}'
      ;;
    /v3/domains/domain-guid) printf '{"guid":"domain-guid","name":"identity.invalid","internal":false}' ;;
    '/v3/routes?hosts=smoke-fixed')
      if [[ -f "$FAKE_STATE/cf-deleted" ]]; then printf '{"resources":[]}'
      else printf '{"resources":[{"guid":"route-guid"}]}'
      fi
      ;;
    *) printf '{"resources":[]}' ;;
  esac
elif [[ ${1:-} == delete && ${2:-} == smoke-fixed ]]; then
  touch "$FAKE_STATE/cf-deleted"
fi
EOF
  chmod +x "$FAKE_BIN/curl" "$FAKE_BIN/cf"
}

run_smoke() {
  PATH="$FAKE_BIN:$PATH" FAKE_LOG="$LOG" FAKE_STATE="$STATE" \
    SMOKE_LIVE=1 SMOKE_NAME=smoke-fixed MANAGER_URL=https://manager.invalid \
    MANAGER_API_TOKEN="$SECRET" SMOKE_REPOSITORY=https://example.invalid/repo.git \
    SMOKE_BUILDPACK=binary_buildpack IDENTITY_DOMAIN=identity.invalid \
    MANAGER_APP_GUID=manager-guid SMOKE_POLL_INTERVAL=0 SMOKE_TIMEOUT=5 \
    SMOKE_SKIP_WRONG_IDENTITY=1 bash "$SCRIPT" 2>&1
}

test_live_execution_is_deferred_by_default() {
  local output status
  set +e
  output=$(SMOKE_LIVE=0 bash "$SCRIPT" 2>&1)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail 'smoke script ran without SMOKE_LIVE=1'
  assert_contains "$output" 'live execution deferred'
}

test_happy_path_uses_safe_auth_and_cleans_up() {
  make_fakes
  local output
  output=$(run_smoke) || fail "happy smoke failed: $output"
  assert_contains "$output" 'SKIP wrong app identity'
  assert_contains "$output" 'PASS smoke-fixed'
  assert_not_contains "$output" "$SECRET"
  local commands
  commands=$(<"$LOG")
  assert_not_contains "$commands" "$SECRET"
  assert_contains "$commands" '<--request> <DELETE>'
  assert_contains "$commands" 'cf <curl> </v3/apps/sandbox-guid/routes>'
  if [[ "$commands" == *'cf <map-route>'* ]]; then fail 'smoke mapped a public route'; fi
}

test_wrong_identity_probe_is_required_by_default() {
  make_fakes
  local output status
  set +e
  output=$(PATH="$FAKE_BIN:$PATH" FAKE_LOG="$LOG" FAKE_STATE="$STATE" \
    SMOKE_LIVE=1 SMOKE_NAME=smoke-fixed MANAGER_URL=https://manager.invalid \
    MANAGER_API_TOKEN="$SECRET" SMOKE_REPOSITORY=https://example.invalid/repo.git \
    SMOKE_BUILDPACK=binary_buildpack IDENTITY_DOMAIN=identity.invalid \
    MANAGER_APP_GUID=manager-guid SMOKE_POLL_INTERVAL=0 SMOKE_TIMEOUT=5 \
    bash "$SCRIPT" 2>&1)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail 'smoke passed without a wrong-identity probe'
  assert_contains "$output" 'WRONG_IDENTITY_PROBE_CMD is required'
  assert_not_contains "$output" "$SECRET"
}

test_manager_cleanup_failure_falls_back_to_direct_cf_cleanup() {
  make_fakes
  local output status
  set +e
  output=$(FAKE_MANAGER_DELETE_STATUS=500 run_smoke)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail 'manager deletion failure unexpectedly passed'
  local commands
  commands=$(<"$LOG")
  assert_contains "$commands" 'cf <remove-route-policy> <identity.invalid> <--hostname> <smoke-fixed> <--source> <cf:app:manager-guid>'
  assert_contains "$commands" 'cf <delete-route> <identity.invalid> <--hostname> <smoke-fixed> <-f>'
  assert_contains "$commands" 'cf <delete> <smoke-fixed> <-f> <-r>'
  assert_not_contains "$output" "$SECRET"
}

test_live_execution_is_deferred_by_default
test_happy_path_uses_safe_auth_and_cleans_up
test_wrong_identity_probe_is_required_by_default
test_manager_cleanup_failure_falls_back_to_direct_cf_cleanup
printf 'PASS smoke script tests\n'
