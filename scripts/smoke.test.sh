#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT="$ROOT/scripts/smoke.sh"
DOC="$ROOT/docs/smoke-test.md"
SECRET='manager-token-must-not-leak'
TEST_DIR=

cleanup_test_dir() { [[ -z ${TEST_DIR:-} ]] || rm -rf "$TEST_DIR"; }
trap cleanup_test_dir EXIT
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_contains() { case "$1" in *"$2"*) ;; *) fail "expected: $2" ;; esac; }
assert_not_contains() { case "$1" in *"$2"*) fail "forbidden output: $2" ;; *) ;; esac; }
assert_ordered() {
  local text=$1 needle position=0 next
  shift
  for needle in "$@"; do
    next=$(printf '%s\n' "$text" | jq -Rrs --arg needle "$needle" --argjson position "$position" '
      [splits("\n")]|to_entries|map(select(.key >= $position and (.value|contains($needle))))|.[0].key//-1
    ')
    (( next >= position )) || fail "command sequence missing or out of order: $needle"
    position=$((next + 1))
  done
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
  [[ $arg != *manager-token-must-not-leak* ]] || exit 91
  printf ' <%s>' "$arg" >>"$FAKE_LOG"
done
printf '\n' >>"$FAKE_LOG"
method=GET output= data_file= url=${!#} previous=
for arg in "$@"; do
  [[ $previous == --request ]] && method=$arg
  [[ $previous == --output ]] && output=$arg
  [[ $previous == --data-binary ]] && data_file=${arg#@}
  previous=$arg
done
status=200 body=
case "$url $method" in
  */manager/api/session\ POST) status=204 ;;
  */manager/api/sandboxes\ POST)
    touch "$FAKE_STATE/created"
    touch "$FAKE_STATE/cf-resource-created"
    if [[ ${FAKE_SCENARIO:-happy} == create_lost ]]; then status=000
    elif [[ ${FAKE_SCENARIO:-happy} == create_malformed ]]; then body='{bad'; status=202
    else body='{"name":"smoke-fixed","repository":"https://example.invalid/repo.git","buildpack":"binary_buildpack","desired":"present","phase":"creating","createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:00Z"}'; status=202
    fi
    ;;
  */manager/api/sandboxes/smoke-fixed\ DELETE)
    touch "$FAKE_STATE/delete-requested"
    if [[ ${FAKE_SCENARIO:-happy} == create_lost || ${FAKE_SCENARIO:-happy} == create_malformed ]]; then status=404
    else status=${FAKE_MANAGER_DELETE_STATUS:-202}
    fi
    [[ $status == 202 && ${FAKE_SCENARIO:-happy} != failed_record_sticks ]] && touch "$FAKE_STATE/manager-cleaned"
    ;;
  */manager/api/sandboxes\ GET)
    if [[ ! -f "$FAKE_STATE/created" && ${FAKE_SCENARIO:-happy} == orphan_manager ]]; then body='[{"name":"smoke-fixed","repository":"https://example.invalid/orphan.git","buildpack":"binary_buildpack","desired":"present","phase":"failed","lastError":"orphan","createdAt":"2026-09-03T00:00:00Z","updatedAt":"2026-09-03T00:01:00Z"}]'
    elif [[ ! -f "$FAKE_STATE/created" && ${FAKE_SCENARIO:-happy} == manager_preflight_bad ]]; then body='{}'
    elif [[ ! -f "$FAKE_STATE/created" ]]; then body='[]'
    elif [[ ${FAKE_SCENARIO:-happy} == malformed ]]; then body='{bad';
    elif [[ -f "$FAKE_STATE/manager-cleaned" && ${FAKE_SCENARIO:-happy} == malformed_deletion_list ]]; then body='{}'
    elif [[ -f "$FAKE_STATE/manager-cleaned" ]]; then body='[]'
    elif [[ ${FAKE_SCENARIO:-happy} == failed || ${FAKE_SCENARIO:-happy} == failed_record_sticks ]]; then body='[{"name":"smoke-fixed","phase":"failed","lastError":"safe failure"}]'
    elif [[ ${FAKE_SCENARIO:-happy} == timeout ]]; then body='[{"name":"smoke-fixed","phase":"creating"}]'
    elif [[ ${FAKE_SCENARIO:-happy} == api_leak ]]; then body='[{"name":"smoke-fixed","phase":"creating","appGuid":"private-guid"}]'
    else body='[{"name":"smoke-fixed","repository":"https://example.invalid/repo.git","buildpack":"binary_buildpack","desired":"present","phase":"ready","packMemberId":"smoke-fixed","operations":[{"name":"stage","duration":3000000000,"success":true},{"name":"start-app","duration":4000000000,"success":true},{"name":"secure-route","duration":5000000000,"success":true},{"name":"trigger-enrollment","duration":6000000000,"success":true}],"createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:07Z"}]'
    fi
    ;;
  */collie/api/snapshot?host=smoke-fixed\ GET)
    if [[ -f "$FAKE_STATE/manager-cleaned" ]]; then body='{"servers":[{"id":"lead","reachable":true}],"workspaces":[],"agents":[],"shellPanes":[],"tabs":[],"sessions":[],"bridge":{},"ts":1}'
    elif [[ -f "$FAKE_STATE/workspace-created" ]]; then body='{"servers":[{"id":"lead","reachable":true},{"id":"smoke-fixed","reachable":true,"protocol":"ok"}],"workspaces":[{"workspaceId":"w2","label":"smoke","host":"smoke-fixed"}],"agents":[],"shellPanes":[{"paneId":"w2:t1:p1","workspaceId":"w2","host":"smoke-fixed"}],"tabs":[],"sessions":[],"bridge":{},"ts":1}'
    else body='{"servers":[{"id":"lead","reachable":true},{"id":"smoke-fixed","reachable":true,"protocol":"ok"}],"workspaces":[],"agents":[],"shellPanes":[],"tabs":[],"sessions":[],"bridge":{},"ts":1}'
    fi
    ;;
  */collie/api/workspace?host=smoke-fixed\ POST)
    [[ -n $data_file ]] || exit 92
    jq -e '. == {cwd:"/home/vcap/app",label:"smoke"}' "$data_file" >/dev/null || exit 93
    touch "$FAKE_STATE/workspace-created"
    body='{"ok":true,"pane":{"paneId":"w2:t1:p1","workspaceId":"w2","workspaceLabel":"smoke","tabId":"w2:t1","cwd":"/home/vcap/app"}}'
    ;;
  */collie/api/pane/w2%3At1%3Ap1/reply?host=smoke-fixed\ POST)
    [[ -n $data_file ]] || exit 92
    jq -e '. == {text:"printf '\''smoke-ready\\n'\''",submit:true}' "$data_file" >/dev/null || exit 93
    touch "$FAKE_STATE/pane-action"
    body='{"ok":true}'
    ;;
  */collie/api/pane/w2%3At1%3Ap1?host=smoke-fixed\ GET)
    [[ -f "$FAKE_STATE/pane-action" ]] || { status=409; body='{"error":"action not sent"}'; }
    [[ $status == 409 ]] || body='{"paneId":"w2:t1:p1","text":"$ printf '\''smoke-ready\\n'\''\nsmoke-ready\n","truncated":false,"revision":"2"}'
    ;;
  */collie/api/pack\ GET)
    if [[ -f "$FAKE_STATE/manager-cleaned" ]]; then body='{"members":[]}'
    else body='{"members":[{"id":"smoke-fixed","health":"reachable"}]}'
    fi
    ;;
  https://smoke-fixed.identity.invalid/pack/v1/hello\ GET) status=404 ;;
  *) status=500; body='{"error":"unexpected fake curl request"}' ;;
esac
printf '%s' "$body" >"$output"
printf '%s' "$status"
case "$status" in 2??) exit 0 ;; *) exit 22 ;; esac
EOF

  CF_CLI="$TEST_DIR/custom-cf"
  cat >"$CF_CLI" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'cf' >>"$FAKE_LOG"; for arg in "$@"; do printf ' <%s>' "$arg" >>"$FAKE_LOG"; done; printf '\n' >>"$FAKE_LOG"
case "${1:-}" in
  target) printf 'api endpoint: https://api.invalid\nuser: smoke-user\norg: smoke-org\nspace: smoke-space\n' ;;
  help) [[ ${2:-} == route-policies ]] ;;
  app)
    case "${2:-}" in manager-app) printf 'name: manager-app\nguid: manager-guid\n' ;; wrong-app) printf 'name: wrong-app\nguid: wrong-guid\n' ;; *) exit 1 ;; esac
    ;;
  ssh)
    app=$2
    if [[ $app == wrong-app ]]; then printf '%s\n' "${FAKE_WRONG_STATUS:-403}"
    elif [[ $app == manager-app ]]; then
      command=${4:-}
      [[ $command == *'./sandbox/runtime/bin/collie pack status'* ]] || { printf 'plain manager curl forbidden\n' >&2; exit 95; }
      [[ $command == *'HERDR_PLUGIN_CONFIG_DIR=./data/collie-config'* && $command == *'HERDR_PLUGIN_STATE_DIR=./data/collie-state'* && $command == *'COLLIE_STATE_DIR=./data/collie-state'* ]] || exit 96
      printf '%s\n' "${FAKE_MANAGER_PACK_STATUS:-mode   lead$'\n'  smoke-fixed  (peer)  link    reachable$'\n'    data    served a snapshot}"
    else exit 1
    fi
    ;;
  curl)
    case "${2:-}" in
      '/v3/domains?names=identity.invalid') printf '{"resources":[{"guid":"domain-guid","name":"identity.invalid"}]}' ;;
      '/v3/apps?names=manager-app') printf '{"resources":[{"guid":"manager-guid","name":"manager-app"}]}' ;;
      '/v3/apps?names=wrong-app')
        if [[ ${FAKE_SCENARIO:-happy} == same_identity ]]; then printf '{"resources":[{"guid":"manager-guid","name":"wrong-app"}]}'
        else printf '{"resources":[{"guid":"wrong-guid","name":"wrong-app"}]}'
        fi ;;
      '/v3/apps?names=smoke-fixed')
        if [[ ${FAKE_SCENARIO:-happy} == preexisting_app ]]; then printf '{"resources":[{"guid":"old-guid","name":"smoke-fixed"}]}'
        elif [[ -f "$FAKE_STATE/manager-cleaned" || -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[]}'
        elif [[ -f "$FAKE_STATE/cf-resource-created" ]]; then printf '{"resources":[{"guid":"sandbox-guid","name":"smoke-fixed"}]}'
        else printf '{"resources":[]}'
        fi ;;
      /v3/apps/sandbox-guid/routes)
        if [[ ${FAKE_SCENARIO:-happy} == malformed_routes ]]; then printf '{bad'
        else printf '{"resources":[{"guid":"route-guid","host":"smoke-fixed","relationships":{"domain":{"data":{"guid":"domain-guid"}}}}]}'
        fi ;;
      /v3/domains/domain-guid) printf '{"guid":"domain-guid","name":"identity.invalid"}' ;;
      '/v3/routes?hosts=smoke-fixed')
        if [[ ${FAKE_SCENARIO:-happy} == preexisting_route ]]; then printf '{"resources":[{"guid":"old-route","host":"smoke-fixed","relationships":{"domain":{"data":{"guid":"domain-guid"}}}}]}'
        elif [[ ${FAKE_SCENARIO:-happy} == leaked_route && ! -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[{"guid":"leak"}]}'
        elif [[ -f "$FAKE_STATE/manager-cleaned" || -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[]}'
        else printf '{"resources":[{"guid":"route-guid"}]}'
        fi ;;
      '/v3/routes?hosts=smoke-fixed&domain_guids=domain-guid')
        if [[ ${FAKE_SCENARIO:-happy} == preexisting_route ]]; then printf '{"resources":[{"guid":"old-route"}]}'
        elif [[ -f "$FAKE_STATE/cf-resource-created" && ! -f "$FAKE_STATE/manager-cleaned" && ! -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[{"guid":"route-guid","host":"smoke-fixed"}]}'
        else printf '{"resources":[]}'
        fi ;;
      '/v3/routes?hosts=manager-pack&domain_guids=domain-guid') printf '{"resources":[{"guid":"manager-route-guid","host":"manager-pack","relationships":{"domain":{"data":{"guid":"domain-guid"}}}}]}' ;;
      '/v3/route_policies?route_guids=route-guid&sources=cf%3Aapp%3Amanager-guid')
        [[ ${FAKE_SCENARIO:-happy} != policy_query_failure ]] || exit 1
        if [[ ${FAKE_SCENARIO:-happy} == policy_empty_object ]]; then printf '{}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_wrong_resources ]]; then printf '{"resources":{}}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_missing_field ]]; then printf '{"resources":[{"source":"cf:app:manager-guid","relationships":{"route":{"data":{}}}}]}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_wrong_relationships ]]; then printf '{"resources":[{"source":"cf:app:manager-guid","relationships":{}}]}'
        elif [[ ${FAKE_SCENARIO:-happy} == leak_sandbox_policy && ! -f "$FAKE_STATE/direct-cleaned" ]]; then
          printf '{"resources":[{"source":"cf:app:manager-guid","relationships":{"route":{"data":{"guid":"route-guid"}}}}]}'
        elif [[ -f "$FAKE_STATE/manager-cleaned" || -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[]}'
        else printf '{"resources":[{"source":"cf:app:manager-guid","relationships":{"route":{"data":{"guid":"route-guid"}}}}]}'
        fi ;;
      '/v3/route_policies?route_guids=manager-route-guid&sources=cf%3Aapp%3Asandbox-guid')
        [[ ${FAKE_SCENARIO:-happy} != policy_query_failure ]] || exit 1
        if [[ ${FAKE_SCENARIO:-happy} == policy_empty_object ]]; then printf '{}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_wrong_resources ]]; then printf '{"resources":{}}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_missing_field ]]; then printf '{"resources":[{"source":"cf:app:sandbox-guid","relationships":{"route":{"data":{}}}}]}'
        elif [[ ${FAKE_SCENARIO:-happy} == policy_wrong_relationships ]]; then printf '{"resources":[{"source":"cf:app:sandbox-guid"}]}'
        elif [[ ${FAKE_SCENARIO:-happy} == leak_manager_policy && ! -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[{"source":"cf:app:sandbox-guid","relationships":{"route":{"data":{"guid":"manager-route-guid"}}}}]}'
        elif [[ -f "$FAKE_STATE/manager-cleaned" || -f "$FAKE_STATE/direct-cleaned" ]]; then printf '{"resources":[]}'
        else printf '{"resources":[{"source":"cf:app:sandbox-guid","relationships":{"route":{"data":{"guid":"manager-route-guid"}}}}]}'
        fi ;;
      /routing/v1/route_policies|/v3/route_policies) exit 97 ;;
      *) printf '{"resources":[]}' ;;
    esac ;;
  remove-route-policy|unmap-route|delete-route|delete) touch "$FAKE_STATE/direct-cleaned" ;;
  *) exit 94 ;;
esac
EOF
  chmod +x "$FAKE_BIN/curl" "$CF_CLI"
}

run_smoke() {
  PATH="$FAKE_BIN:$PATH" TMPDIR=/tmp FAKE_LOG="$LOG" FAKE_STATE="$STATE" CF_BIN="${CF_BIN:-$CF_CLI}" \
    SMOKE_LIVE=1 SMOKE_NAME=smoke-fixed MANAGER_URL=https://manager.invalid \
    MANAGER_API_TOKEN="$SECRET" MANAGER_APP_NAME=manager-app WRONG_IDENTITY_APP=wrong-app \
    SMOKE_REPOSITORY=https://example.invalid/repo.git SMOKE_BUILDPACK=binary_buildpack \
    SMOKE_WORKSPACE_CWD=/home/vcap/app IDENTITY_DOMAIN=identity.invalid \
    MANAGER_APP_GUID=manager-guid MANAGER_ROUTE_HOST=manager-pack SMOKE_POLL_INTERVAL=0 SMOKE_TIMEOUT=${SMOKE_TIMEOUT:-2} SMOKE_CLEANUP_TIMEOUT=${SMOKE_CLEANUP_TIMEOUT:-2} \
    "$BASH" "$SCRIPT" 2>&1
}

test_explicit_cf_bin_works_without_cf_in_path() {
  make_fakes
  local output commands tool tool_bin="$TEST_DIR/tools"
  mkdir -p "$tool_bin"
  for tool in awk bash chmod cp date env jq mktemp rm sleep touch; do
    ln -s "$(command -v "$tool")" "$tool_bin/$tool"
  done
  output=$(PATH="$tool_bin" run_smoke) || fail "custom CF_BIN failed: $output"
  commands=$(<"$LOG")
  assert_contains "$output" 'PASS smoke-fixed'
  assert_contains "$commands" 'cf <target>'
  [[ ! -e "$FAKE_BIN/cf" ]] || fail 'test PATH unexpectedly contains cf'
}

test_missing_cf_error_is_actionable() {
  make_fakes
  local output status tool_bin="$TEST_DIR/tools"
  mkdir -p "$tool_bin"
  ln -s "$(command -v bash)" "$tool_bin/bash"
  ln -s "$(command -v env)" "$tool_bin/env"
  ln -s "$(command -v jq)" "$tool_bin/jq"
  set +e
  output=$(PATH="$tool_bin" CF_BIN=/missing/custom-cf run_smoke)
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail 'missing custom CF_BIN unexpectedly passed'
  assert_contains "$output" 'CF_BIN must name an executable file: /missing/custom-cf'
}

test_smoke_has_no_bare_cf_invocations() {
  local line
  while IFS= read -r line; do
    if [[ $line =~ ^[[:space:]]*(cf[[:space:]]|[^#]*\$\(cf[[:space:]]) ]]; then
      fail "bare cf invocation: $line"
    fi
  done <"$SCRIPT"
}

run_failure() {
  local expect_cleanup=${1:-yes}
  local output status
  set +e; output=$(run_smoke); status=$?; set -e
  [[ $status -ne 0 ]] || fail "scenario ${FAKE_SCENARIO:-unknown} unexpectedly passed"
  assert_not_contains "$output" "$SECRET"
  [[ $expect_cleanup == no || -f "$STATE/delete-requested" ]] || fail 'manager cleanup was not requested'
  printf '%s' "$output"
}

test_live_guard_prevents_commands() {
  make_fakes
  local output status
  set +e; output=$(SMOKE_LIVE=0 PATH="$FAKE_BIN:$PATH" FAKE_LOG="$LOG" FAKE_STATE="$STATE" bash "$SCRIPT" 2>&1); status=$?; set -e
  [[ $status -ne 0 ]] || fail 'unguarded smoke passed'
  assert_contains "$output" 'live execution deferred'
  [[ ! -e $LOG ]] || fail 'guard invoked a command'
}

test_full_flow_and_exact_identity_checks() {
  make_fakes
  local output commands
  output=$(run_smoke) || fail "happy path failed: $output"
  commands=$(<"$LOG")
  assert_contains "$output" 'PASS smoke-fixed'
  assert_contains "$output" 'clone'
  assert_contains "$output" 'upload'
  assert_contains "$output" 'policy'
  assert_contains "$output" 'Pack'
  assert_contains "$output" 'delete'
  assert_contains "$output" 'clone                unavailable'
  assert_contains "$output" 'upload               unavailable'
  assert_not_contains "$output" "$SECRET"
  assert_not_contains "$commands" "$SECRET"
  assert_not_contains "$commands" '<--insecure>'
  assert_not_contains "$commands" '<--cacert>'
  assert_contains "$commands" 'cf <ssh> <wrong-app>'
  assert_contains "$commands" 'cf <ssh> <manager-app>'
  assert_contains "$commands" './sandbox/runtime/bin/collie pack status'
  assert_not_contains "$commands" 'manager-app> <-c> <file=$(mktemp)'
  assert_contains "$commands" '<https://manager.invalid/collie/api/snapshot?host=smoke-fixed>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/workspace?host=smoke-fixed>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/pane/w2%3At1%3Ap1/reply?host=smoke-fixed>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/pane/w2%3At1%3Ap1?host=smoke-fixed>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/pack>'
  assert_contains "$commands" 'cf <curl> </v3/route_policies?route_guids=route-guid&sources=cf%3Aapp%3Amanager-guid>'
  assert_contains "$commands" 'cf <curl> </v3/route_policies?route_guids=manager-route-guid&sources=cf%3Aapp%3Asandbox-guid>'
  assert_not_contains "$commands" '/routing/v1/route_policies'
  [[ -f "$STATE/workspace-created" ]] || fail 'workspace was not created'
  [[ -f "$STATE/pane-action" ]] || fail 'pane action was not sent'
  assert_ordered "$commands" \
    '/manager/api/sandboxes>' \
    '/manager/api/sandboxes>' \
    'cf <ssh> <wrong-app>' \
    'cf <ssh> <manager-app>' \
    '/collie/api/snapshot?host=smoke-fixed>' \
    '/collie/api/workspace?host=smoke-fixed>' \
    '/collie/api/snapshot?host=smoke-fixed>' \
    '/collie/api/pane/w2%3At1%3Ap1/reply?host=smoke-fixed>' \
    '/collie/api/pane/w2%3At1%3Ap1?host=smoke-fixed>' \
    '/manager/api/sandboxes/smoke-fixed>' \
    '/collie/api/snapshot?host=smoke-fixed>' \
    '/collie/api/pack>' \
    'cf <curl> </v3/apps?names=smoke-fixed>'
  if [[ $commands == *'cf <map-route>'* ]]; then fail 'mapped public route'; fi
}

test_public_ca_is_limited_to_manager_curl() {
  make_fakes
  local ca_cert="$TEST_DIR/lab-ca.pem" output commands
  touch "$ca_cert"
  output=$(SMOKE_PUBLIC_CA_CERT="$ca_cert" run_smoke) || fail "CA mode failed: $output"
  commands=$(<"$LOG")
  assert_contains "$commands" "curl <--cacert> <$ca_cert> <--silent>"
  assert_contains "$commands" '<https://manager.invalid/manager/api/session>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/snapshot?host=smoke-fixed>'
  assert_contains "$commands" 'curl <--silent> <--show-error> <--output>'
  assert_contains "$commands" '<https://smoke-fixed.identity.invalid/pack/v1/hello>'
  assert_not_contains "$commands" "cf <ssh> <wrong-app> <-c> <code=\$(curl --cacert"
  assert_not_contains "$commands" 'cf <ssh> <wrong-app> <-c> <code=$(curl --insecure'
  assert_not_contains "$commands" 'cf <ssh> <manager-app> <-c> <HERDR_PLUGIN_CONFIG_DIR=./data/collie-config --cacert'
}

test_insecure_public_tls_is_explicit_and_limited_to_manager_curl() {
  make_fakes
  local output commands warning_count
  output=$(SMOKE_INSECURE_PUBLIC_TLS=1 run_smoke) || fail "insecure mode failed: $output"
  commands=$(<"$LOG")
  warning_count=$(printf '%s\n' "$output" | jq -Rrs '[splits("\n")|select(.=="smoke: public-route TLS verification disabled for lab")]|length')
  [[ $warning_count == 1 ]] || fail "expected exactly one insecure TLS warning, got $warning_count"
  assert_contains "$commands" 'curl <--insecure> <--silent>'
  assert_contains "$commands" '<https://manager.invalid/manager/api/session>'
  assert_contains "$commands" '<https://manager.invalid/collie/api/snapshot?host=smoke-fixed>'
  assert_contains "$commands" 'curl <--silent> <--show-error> <--output>'
  assert_contains "$commands" '<https://smoke-fixed.identity.invalid/pack/v1/hello>'
  assert_not_contains "$commands" 'cf <ssh> <wrong-app> <-c> <code=$(curl --insecure'
  assert_not_contains "$commands" 'cf <ssh> <wrong-app> <-c> <code=$(curl --cacert'
  assert_not_contains "$commands" 'cf <ssh> <manager-app> <-c> <HERDR_PLUGIN_CONFIG_DIR=./data/collie-config --insecure'
}

test_public_tls_configuration_fails_closed() {
  make_fakes
  local ca_cert="$TEST_DIR/lab-ca.pem" output commands
  touch "$ca_cert"
  output=$(SMOKE_INSECURE_PUBLIC_TLS=true run_failure no)
  assert_contains "$output" 'SMOKE_INSECURE_PUBLIC_TLS must be 1 or unset'
  [[ ! -e $LOG ]] || fail 'invalid TLS mode invoked a command'

  make_fakes
  ca_cert="$TEST_DIR/lab-ca.pem"
  touch "$ca_cert"
  output=$(SMOKE_PUBLIC_CA_CERT="$ca_cert" SMOKE_INSECURE_PUBLIC_TLS=1 run_failure no)
  assert_contains "$output" 'SMOKE_PUBLIC_CA_CERT conflicts with SMOKE_INSECURE_PUBLIC_TLS=1'

  make_fakes
  output=$(SMOKE_PUBLIC_CA_CERT="$TEST_DIR/missing.pem" run_failure no)
  assert_contains "$output" 'SMOKE_PUBLIC_CA_CERT must be a readable regular non-symlink file'

  make_fakes
  touch "$TEST_DIR/ca-target.pem"
  ln -s "$TEST_DIR/ca-target.pem" "$TEST_DIR/ca-link.pem"
  output=$(SMOKE_PUBLIC_CA_CERT="$TEST_DIR/ca-link.pem" run_failure no)
  assert_contains "$output" 'SMOKE_PUBLIC_CA_CERT must be a readable regular non-symlink file'
}

test_docs_require_complete_live_prerequisites() {
  local docs
  docs=$(<"$DOC")
  assert_contains "$docs" 'WRONG_IDENTITY_APP'
  assert_contains "$docs" 'MANAGER_APP_NAME'
  assert_contains "$docs" 'CF_BIN=/etc/profiles/per-user/$USER/bin/cf'
  assert_contains "$docs" 'POST `/collie/api/workspace?host=<member>`'
  assert_contains "$docs" 'exactly HTTP 403'
  assert_contains "$docs" 'packaged Collie `pack status`'
  assert_contains "$docs" 'SMOKE_CLEANUP_TIMEOUT'
  assert_contains "$docs" 'work root initialization'
  assert_contains "$docs" 'residual manager record'
  assert_contains "$docs" '**In progress.**'
  assert_not_contains "$docs" 'SMOKE_SKIP_WRONG_IDENTITY'
  assert_not_contains "$docs" 'read-only'
}

test_identity_status_must_be_exact() {
  make_fakes; local output
  output=$(FAKE_WRONG_STATUS=404 run_failure); assert_contains "$output" 'wrong identity expected HTTP 403'
  make_fakes; output=$(FAKE_MANAGER_PACK_STATUS='mode   lead' run_failure); assert_contains "$output" 'authenticated Pack status lacks reachable member'
  make_fakes
  output=$(FAKE_MANAGER_PACK_STATUS=$'  smoke-fixed  (peer)  link    unreachable\n  other  (peer)  link    reachable' run_failure)
  assert_contains "$output" 'authenticated Pack status lacks reachable member'
}

test_bad_lifecycle_and_security_responses_fail_closed() {
  local scenario output
  for scenario in malformed failed api_leak malformed_routes policy_query_failure leaked_route leak_sandbox_policy leak_manager_policy; do
    make_fakes
    output=$(FAKE_SCENARIO=$scenario run_failure)
    case $scenario in
      malformed) assert_contains "$output" 'invalid manager JSON' ;;
      failed) assert_contains "$output" 'lifecycle failed' ;;
      api_leak) assert_contains "$output" 'forbidden key' ;;
      malformed_routes) assert_contains "$output" 'invalid sandbox route JSON' ;;
      policy_query_failure) assert_contains "$output" 'route-policy query failed' ;;
      leaked_route) assert_contains "$output" 'route remains after deletion' ;;
      leak_sandbox_policy) assert_contains "$output" 'sandbox route policy remains after deletion' ;;
      leak_manager_policy) assert_contains "$output" 'manager enrollment policy remains after deletion' ;;
    esac
    if [[ $scenario == leaked_route && ! -f "$STATE/direct-cleaned" ]]; then fail 'leaked route did not trigger direct cleanup'; fi
  done
  make_fakes
  output=$(FAKE_SCENARIO=timeout SMOKE_TIMEOUT=1 run_failure)
  assert_contains "$output" 'lifecycle deadline exceeded'

  make_fakes
  output=$(FAKE_SCENARIO=same_identity run_failure no)
  assert_contains "$output" 'wrong-identity app must differ from manager'
}

test_create_response_failure_still_cleans_by_name() {
  local scenario output commands
  for scenario in create_lost create_malformed; do
    make_fakes
    output=$(FAKE_SCENARIO=$scenario run_failure)
    commands=$(<"$LOG")
    [[ -f "$STATE/cf-resource-created" ]] || fail 'fake did not create resource before response failure'
    assert_contains "$commands" '/manager/api/sandboxes/smoke-fixed>'
    assert_contains "$commands" 'cf <delete> <smoke-fixed>'
    assert_not_contains "$output" "$SECRET"
  done
}

test_preexisting_name_aborts_without_cleanup() {
  local scenario output commands
  for scenario in preexisting_app preexisting_route; do
    make_fakes
    output=$(FAKE_SCENARIO=$scenario run_failure no)
    commands=$(<"$LOG")
    assert_contains "$output" 'sandbox name already has CF resources'
    assert_not_contains "$commands" '/manager/api/sandboxes/smoke-fixed>'
    assert_not_contains "$commands" 'cf <remove-route-policy>'
    assert_not_contains "$commands" 'cf <delete-route>'
    assert_not_contains "$commands" 'cf <delete> <smoke-fixed>'
  done
}

test_orphan_manager_record_aborts_without_cleanup() {
  make_fakes
  local output commands
  output=$(FAKE_SCENARIO=orphan_manager run_failure no)
  commands=$(<"$LOG")
  assert_contains "$output" 'sandbox name already has a manager record'
  assert_contains "$commands" '<--request> <GET>'
  assert_not_contains "$commands" '<--request> <DELETE>'
  assert_not_contains "$commands" 'cf <remove-route-policy>'
  assert_not_contains "$commands" 'cf <delete-route>'
  assert_not_contains "$commands" 'cf <delete> <smoke-fixed>'
}

test_manager_preflight_schema_is_strict() {
  make_fakes
  local output commands
  output=$(FAKE_SCENARIO=manager_preflight_bad run_failure no)
  commands=$(<"$LOG")
  assert_contains "$output" 'manager sandbox preflight returned unexpected schema'
  assert_not_contains "$commands" '<--request> <DELETE>'
  assert_not_contains "$commands" 'cf <delete> <smoke-fixed>'
}

test_manager_deletion_list_schema_is_strict() {
  make_fakes
  local output commands
  output=$(FAKE_SCENARIO=malformed_deletion_list run_failure)
  commands=$(<"$LOG")
  assert_contains "$output" 'manager sandbox deletion response returned unexpected schema'
  assert_contains "$commands" 'cf <delete> <smoke-fixed>'
}

test_route_policy_schema_failures_are_not_absence() {
  local scenario output
  for scenario in policy_empty_object policy_wrong_resources policy_missing_field policy_wrong_relationships; do
    make_fakes
    output=$(FAKE_SCENARIO=$scenario run_failure)
    assert_contains "$output" 'route-policy query returned unexpected schema'
  done
}

test_cleanup_is_armed_immediately_before_create() {
  local script arm_line post_line
  script=$(<"$SCRIPT")
  arm_line=$(jq -Rrs 'split("\n")|to_entries|map(select(.value=="cleanup_armed=1"))|.[0].key' <<<"$script")
  post_line=$(jq -Rrs 'split("\n")|to_entries|map(select(.value|contains("gateway_status POST") and contains("/manager/api/sandboxes")))|.[0].key' <<<"$script")
  (( post_line > arm_line && post_line - arm_line <= 3 )) || fail 'cleanup is not armed immediately before create POST'
}

test_cleanup_falls_back_to_direct_cf() {
  make_fakes
  local output commands
  output=$(FAKE_MANAGER_DELETE_STATUS=500 run_failure)
  commands=$(<"$LOG")
  assert_contains "$commands" 'cf <remove-route-policy>'
  assert_contains "$commands" '<manager-pack>'
  assert_contains "$commands" 'cf <delete-route>'
  assert_contains "$commands" 'cf <delete> <smoke-fixed>'
}

test_failed_lifecycle_cleanup_is_independently_bounded() {
  make_fakes
  local output commands started=$SECONDS elapsed
  output=$(FAKE_SCENARIO=failed_record_sticks SMOKE_TIMEOUT=1200 SMOKE_CLEANUP_TIMEOUT=1 run_failure)
  elapsed=$((SECONDS - started))
  commands=$(<"$LOG")
  (( elapsed < 5 )) || fail "cleanup used lifecycle timeout (${elapsed}s)"
  assert_contains "$output" 'lifecycle failed: safe failure'
  assert_contains "$output" 'cleanup deadline exceeded; residual manager record: smoke-fixed'
  assert_contains "$commands" 'cf <delete> <smoke-fixed>'
  assert_contains "$commands" '/manager/api/sandboxes/smoke-fixed>'
}

test_cleanup_timeout_must_be_positive() {
  make_fakes
  local output
  output=$(SMOKE_CLEANUP_TIMEOUT=0 run_failure no)
  assert_contains "$output" 'SMOKE_CLEANUP_TIMEOUT must be a positive integer'
  [[ ! -e $LOG ]] || fail 'invalid cleanup timeout invoked a command'
}

test_live_guard_prevents_commands
test_explicit_cf_bin_works_without_cf_in_path
test_missing_cf_error_is_actionable
test_smoke_has_no_bare_cf_invocations
test_full_flow_and_exact_identity_checks
test_public_ca_is_limited_to_manager_curl
test_insecure_public_tls_is_explicit_and_limited_to_manager_curl
test_public_tls_configuration_fails_closed
test_docs_require_complete_live_prerequisites
test_identity_status_must_be_exact
test_bad_lifecycle_and_security_responses_fail_closed
test_create_response_failure_still_cleans_by_name
test_preexisting_name_aborts_without_cleanup
test_orphan_manager_record_aborts_without_cleanup
test_manager_preflight_schema_is_strict
test_manager_deletion_list_schema_is_strict
test_route_policy_schema_failures_are_not_absence
test_cleanup_is_armed_immediately_before_create
test_cleanup_falls_back_to_direct_cf
test_failed_lifecycle_cleanup_is_independently_bounded
test_cleanup_timeout_must_be_positive
printf 'PASS smoke script tests\n'
