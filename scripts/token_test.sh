#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT="$ROOT/scripts/token.sh"
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT

FAKE_BIN="$TEST_DIR/bin"
LOG="$TEST_DIR/cf.log"
mkdir -p "$FAKE_BIN"

cat >"$FAKE_BIN/cf" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'argv' >>"$FAKE_LOG"
for arg in "$@"; do
  printf ' <%s>' "$arg" >>"$FAKE_LOG"
done
printf '\n' >>"$FAKE_LOG"
case "${FAKE_SCENARIO:-happy}:$1" in
  app-fails:app)
    printf 'secret-from-cf-error\n' >&2
    exit 1
    ;;
  bad-guid:app)
    printf 'not-a-uuid\n'
    ;;
  *:app)
    printf '123e4567-e89b-12d3-a456-426614174000\n'
    ;;
  *:curl)
    case "${FAKE_SCENARIO:-happy}" in
      malformed) printf '{broken\n' ;;
      missing) printf '{"environment_variables":{"run":{}}}\n' ;;
      empty) printf '{"environment_variables":{"run":{"MANAGER_API_TOKEN":""}}}\n' ;;
      nonstring) printf '{"environment_variables":{"run":{"MANAGER_API_TOKEN":42}}}\n' ;;
      curl-fails) printf 'secret-from-cf-error\n' >&2; exit 1 ;;
      *) printf '{"environment_variables":{"run":{"MANAGER_API_TOKEN":"manager-token-must-not-leak"}}}\n' ;;
    esac
    ;;
  *:set-env)
    printf 'set-env must not be called\n' >&2
    exit 91
    ;;
esac
EOF
chmod +x "$FAKE_BIN/cf"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
run_token() { env -i PATH="$PATH" FAKE_LOG="$LOG" FAKE_SCENARIO="${1:-happy}" MANAGER_APP_NAME=manager CF_BIN="$FAKE_BIN/cf" bash "$SCRIPT"; }

set +e
output=$(env -i PATH="$PATH" FAKE_LOG="$LOG" CF_BIN="$FAKE_BIN/cf" bash "$SCRIPT" 2>"$TEST_DIR/no-app.err")
status=$?
set -e
(( status != 0 )) || fail 'missing MANAGER_APP_NAME unexpectedly succeeded'
grep -q 'MANAGER_APP_NAME is required' "$TEST_DIR/no-app.err" || fail 'missing app name was not actionable'
[[ -z $output ]] || fail "missing app name wrote stdout: $output"

set +e
output=$(env -i PATH="$PATH" FAKE_LOG="$LOG" MANAGER_APP_NAME=manager CF_BIN="$FAKE_BIN/cf" JQ_BIN="$TEST_DIR/missing-jq" bash "$SCRIPT" 2>"$TEST_DIR/no-jq.err")
status=$?
set -e
(( status != 0 )) || fail 'missing jq unexpectedly succeeded'
grep -q 'jq' "$TEST_DIR/no-jq.err" || fail 'missing jq was not actionable'
[[ -z $output ]] || fail "missing jq wrote stdout: $output"

: >"$LOG"
output=$(run_token)
[[ $output == manager-token-must-not-leak ]] || fail "unexpected token output: $output"
mapfile -t happy_calls <"$LOG"
[[ ${happy_calls[0]:-} == 'argv <app> <manager> <--guid>' ]] || fail "unexpected app lookup: ${happy_calls[0]:-}"
[[ ${happy_calls[1]:-} == 'argv <curl> </v3/apps/123e4567-e89b-12d3-a456-426614174000/env>' ]] || fail "unexpected env lookup: ${happy_calls[1]:-}"

for scenario in app-fails malformed missing empty nonstring curl-fails bad-guid; do
  set +e
  output=$(run_token "$scenario" 2>"$TEST_DIR/$scenario.err")
  status=$?
  set -e
  (( status != 0 )) || fail "$scenario unexpectedly succeeded"
  [[ -z $output ]] || fail "$scenario wrote stdout: $output"
  ! grep -q 'manager-token-must-not-leak\|secret-from-cf-error' "$TEST_DIR/$scenario.err" || fail "$scenario leaked a secret"
done

! grep -q 'set-env' "$LOG" || fail 'set-env was invoked'

printf 'token tests passed\n'
