#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
COLLIE_DIR="$SCRIPT_DIR/collie"
export COLLIE_EXECUTABLE="$BIN_DIR/collie"

SANDBOX_STATE_DIR="${SANDBOX_STATE_DIR:-/home/vcap/app/.sandbox-state}"
export HOME="${SANDBOX_HOME:-$SANDBOX_STATE_DIR/home}"
export PATH="$BIN_DIR:$PATH"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$SANDBOX_STATE_DIR/config}"
export XDG_STATE_HOME="${XDG_STATE_HOME:-$SANDBOX_STATE_DIR/state}"
export XDG_DATA_HOME="${XDG_DATA_HOME:-$SANDBOX_STATE_DIR/data}"
export COLLIE_STATE_DIR="${COLLIE_STATE_DIR:-$XDG_STATE_HOME/collie}"
export COLLIE_PLUGIN_ROOT="$COLLIE_DIR"
export HERDR_PLUGIN_CONFIG_DIR="${HERDR_PLUGIN_CONFIG_DIR:-$XDG_CONFIG_HOME/collie}"
export HERDR_SOCKET_PATH="${HERDR_SOCKET_PATH:-$SANDBOX_STATE_DIR/herdr.sock}"
export COLLIE_MUX="${COLLIE_MUX:-herdr}"
export COLLIE_PORT="${PORT:?Cloud Foundry PORT is required}"
export COLLIE_HOST=0.0.0.0
export COLLIE_ALLOW_NON_LOOPBACK_BIND=1
export COLLIE_PACK_TRANSPORT=cf-identity
export SANDBOX_MEMBER_ID="${SANDBOX_MEMBER_ID:-}"

configure_bashrc() {
	local bashrc="$HOME/.bashrc"
	local block_start='# BEGIN CF HERDR SANDBOX RUNTIME'
	local block_end='# END CF HERDR SANDBOX RUNTIME'
	local temporary
	temporary="$(mktemp "${bashrc}.XXXXXX")"
	if [[ -f "$bashrc" ]]; then
		awk -v start="$block_start" -v end="$block_end" '$0 == start {skip=1; next} $0 == end {skip=0; next} !skip {print}' "$bashrc" >"$temporary"
	fi
	{
		printf '\n%s\n' "$block_start"
		printf 'export PATH=%s:$PATH\n' "$BIN_DIR"
		printf 'export HERDR_SOCKET_PATH=%q\n' "$HERDR_SOCKET_PATH"
		printf '%s\n' "$block_end"
	} >>"$temporary"
	chmod 600 "$temporary"
	mv -- "$temporary" "$bashrc"
}

has_join_token_file=false
has_pack_lead_address=false
[[ -n "${COLLIE_JOIN_TOKEN_FILE:-}" ]] && has_join_token_file=true
[[ -n "${COLLIE_PACK_LEAD_ADDRESS:-}" ]] && has_pack_lead_address=true
if [[ "$has_join_token_file" != "$has_pack_lead_address" ]]; then
	printf 'COLLIE_JOIN_TOKEN_FILE and COLLIE_PACK_LEAD_ADDRESS must be supplied together\n' >&2
	exit 2
fi

mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$COLLIE_STATE_DIR" "$HERDR_PLUGIN_CONFIG_DIR" "$(dirname -- "$HERDR_SOCKET_PATH")"
configure_bashrc

herdr_pid=""
bootstrap_pid=""
collie_pid=""
cleanup() {
	if [[ -n "$bootstrap_pid" ]] && kill -0 "$bootstrap_pid" 2>/dev/null; then
		kill "$bootstrap_pid" 2>/dev/null || true
		wait "$bootstrap_pid" 2>/dev/null || true
		bootstrap_pid=""
	fi
	if [[ -n "$collie_pid" ]] && kill -0 "$collie_pid" 2>/dev/null; then
		kill "$collie_pid" 2>/dev/null || true
		wait "$collie_pid" 2>/dev/null || true
	fi
	if [[ -n "$herdr_pid" ]] && kill -0 "$herdr_pid" 2>/dev/null; then
		kill "$herdr_pid" 2>/dev/null || true
		wait "$herdr_pid" 2>/dev/null || true
	fi
}
terminate() {
	local signal=$1
	local status=$2
	if [[ -n "$collie_pid" ]] && kill -0 "$collie_pid" 2>/dev/null; then
		kill -"$signal" "$collie_pid" 2>/dev/null || true
		wait "$collie_pid" 2>/dev/null || true
		collie_pid=""
	fi
	exit "$status"
}
trap 'terminate TERM 143' TERM
trap 'terminate INT 130' INT
trap cleanup EXIT

"$BIN_DIR/herdr" server &
herdr_pid=$!

deadline=$((SECONDS + ${HERDR_START_TIMEOUT_SECONDS:-30}))
while [[ ! -S "$HERDR_SOCKET_PATH" ]]; do
  if ! kill -0 "$herdr_pid" 2>/dev/null; then
    wait "$herdr_pid"
    exit 1
  fi
  if (( SECONDS >= deadline )); then
    printf 'herdr socket did not appear at %s\n' "$HERDR_SOCKET_PATH" >&2
    exit 1
  fi
  sleep 0.1
done

if [[ "$has_join_token_file" == true ]]; then
  export COLLIE_PACK_SELF_ADDRESS="${COLLIE_PACK_SELF_ADDRESS:-}"
  export COLLIE_PACK_TRUST_STORE="$COLLIE_STATE_DIR/pack-trust.json"
  trust_store="$COLLIE_PACK_TRUST_STORE"
  if [[ -L "$trust_store" ]]; then
    printf 'Pack trust store must not be a symlink: %s\n' "$trust_store" >&2
    exit 1
  fi
  if [[ ! -f "$trust_store" ]]; then
    export SANDBOX_BOOTSTRAP_READY_FILE="${SANDBOX_BOOTSTRAP_READY_FILE:-$SANDBOX_STATE_DIR/bootstrap-ready}"
    rm -f -- "$SANDBOX_BOOTSTRAP_READY_FILE"
    "$BIN_DIR/sandbox-bootstrap" &
    bootstrap_pid=$!
    while [[ ! -f "$SANDBOX_BOOTSTRAP_READY_FILE" && ! -f "$trust_store" ]]; do
      if [[ -L "$trust_store" ]]; then
        printf 'Pack trust store must not be a symlink: %s\n' "$trust_store" >&2
        exit 1
      fi
      if ! kill -0 "$bootstrap_pid" 2>/dev/null; then
        wait "$bootstrap_pid"
        exit 1
      fi
      sleep 0.1
    done
    if [[ -f "$trust_store" && ! -L "$trust_store" ]]; then
      rm -f -- "${COLLIE_JOIN_TOKEN_FILE:-}"
    fi
    kill "$bootstrap_pid" 2>/dev/null || true
    wait "$bootstrap_pid" || true
    bootstrap_pid=""
  fi
fi

(exec "$BIN_DIR/bun" run "$COLLIE_DIR/bridge/index.ts") &
collie_pid=$!
set +e
wait "$collie_pid"
status=$?
set -e
collie_pid=""
exit "$status"
