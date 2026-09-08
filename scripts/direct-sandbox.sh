#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="${BASH_SOURCE[0]%/*}"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"
RUNTIME_DIR="$ROOT_DIR/dist/sandbox/runtime"

resolve_cf() {
  local path=${CF_BIN:-}
  if [[ -z "$path" ]]; then path="$(command -v cf 2>/dev/null || true)"; fi
  if [[ -z "$path" && -n "${LAB_PROFILE_BIN_DIR:-}" && -x "$LAB_PROFILE_BIN_DIR/cf" ]]; then path="$LAB_PROFILE_BIN_DIR/cf"; fi
  if [[ -z "$path" && -n "${USER:-}" && -x "/etc/profiles/per-user/$USER/bin/cf" ]]; then path="/etc/profiles/per-user/$USER/bin/cf"; fi
  if [[ -z "$path" && -n "${HOME:-}" && -x "$HOME/.nix-profile/bin/cf" ]]; then path="$HOME/.nix-profile/bin/cf"; fi
  [[ -n "$path" && -f "$path" && -x "$path" ]] || { printf 'error: required CF CLI was not found; set CF_BIN or expose cf in PATH or a Nix profile\n' >&2; exit 2; }
  printf '%s' "$path"
}

valid_name() { [[ "$1" =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]]; }
valid_buildpack() { [[ "$1" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$ ]]; }
valid_repository() { [[ "$1" =~ ^https?://[^[:space:]]+$ && "$1" != *"'"* && "$1" != *'"'* ]]; }

CF_BIN="$(resolve_cf)"
APP_NAME="${SANDBOX_NAME:-direct-sandbox}"
REPOSITORY="${SANDBOX_REPOSITORY:-https://github.com/cloudfoundry-samples/cf-sample-app-nodejs.git}"
BUILDPACK="${SANDBOX_BUILDPACK:-nodejs_buildpack}"
SANDBOX_CWD="${SANDBOX_CWD:-/home/vcap/app}"
: "${CF_API:?CF_API is required; target the intended API explicitly}"
: "${CF_ORG:?CF_ORG is required; target the intended org explicitly}"
: "${CF_SPACE:?CF_SPACE is required; target the intended space explicitly}"
valid_name "$APP_NAME" || { printf 'error: invalid SANDBOX_NAME\n' >&2; exit 2; }
valid_buildpack "$BUILDPACK" || { printf 'error: invalid SANDBOX_BUILDPACK\n' >&2; exit 2; }
valid_repository "$REPOSITORY" || { printf 'error: SANDBOX_REPOSITORY must be an http(s) URL\n' >&2; exit 2; }
[[ "$SANDBOX_CWD" == /* && "$SANDBOX_CWD" != *$'\n'* ]] || { printf 'error: SANDBOX_CWD must be an absolute path\n' >&2; exit 2; }
[[ -d "$RUNTIME_DIR" && -x "$RUNTIME_DIR/start.sh" ]] || { printf 'error: sandbox runtime artifacts are missing under %s\n' "$RUNTIME_DIR" >&2; exit 2; }
if [[ -n "${COLLIE_JOIN_TOKEN_FILE:-}" || -n "${COLLIE_PACK_LEAD_ADDRESS:-}" ]]; then
  [[ -n "${COLLIE_JOIN_TOKEN_FILE:-}" && -n "${COLLIE_PACK_LEAD_ADDRESS:-}" ]] || { printf 'error: COLLIE_PACK_LEAD_ADDRESS is required with a join token\n' >&2; exit 2; }
  [[ -f "$COLLIE_JOIN_TOKEN_FILE" && ! -L "$COLLIE_JOIN_TOKEN_FILE" && -r "$COLLIE_JOIN_TOKEN_FILE" ]] || { printf 'error: COLLIE_JOIN_TOKEN_FILE must be a readable regular file\n' >&2; exit 2; }
fi
for asset in bin/bun bin/herdr bin/collie bin/sandbox-bootstrap collie/bridge/index.ts collie/package.json; do
  [[ -e "$RUNTIME_DIR/$asset" ]] || { printf 'error: sandbox runtime asset is missing: %s\n' "$asset" >&2; exit 2; }
done

if "$CF_BIN" app "$APP_NAME" --guid >/dev/null 2>&1; then
  printf 'error: app %s already exists; refusing to take ownership\n' "$APP_NAME" >&2
  exit 1
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cf-herdr-direct-sandbox.XXXXXX")"
created=0
cleanup() {
  local status=$?
  if [[ "$created" == 1 && "${SANDBOX_KEEP:-}" != 1 ]]; then
    "$CF_BIN" delete "$APP_NAME" -f -r >/dev/null 2>&1 || true
  fi
  rm -rf -- "$WORK_DIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

git clone --depth 1 -- "$REPOSITORY" "$WORK_DIR/app"
cp -a "$RUNTIME_DIR" "$WORK_DIR/app/.sandbox"
while IFS= read -r -d '' link; do
  target="$(readlink -f -- "$link")"
  case "$target" in "$WORK_DIR/app/.sandbox"/*) ;; *) printf 'error: runtime symlink escapes .sandbox: %s\n' "${link#"$WORK_DIR/app/.sandbox/"}" >&2; exit 2;; esac
  done < <(find "$WORK_DIR/app/.sandbox" -type l -print0)

cat >"$WORK_DIR/app/.sandbox/start.sh" <<'LAUNCHER'
#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export COLLIE_PORT="${PORT:?Cloud Foundry PORT is required}"
export COLLIE_HOST=0.0.0.0
export COLLIE_PACK_TRANSPORT=cf-identity
export COLLIE_PLUGIN_ROOT="$SCRIPT_DIR/collie"
export HERDR_SOCKET_PATH="${HERDR_SOCKET_PATH:-/home/vcap/app/.sandbox-state/herdr.sock}"
export COLLIE_MUX=herdr
mkdir -p "$(dirname -- "$HERDR_SOCKET_PATH")"
"$SCRIPT_DIR/bin/herdr" server &
herdr_pid=$!
trap 'kill "$herdr_pid" 2>/dev/null || true' EXIT INT TERM
deadline=$((SECONDS + 30))
while [[ ! -S "$HERDR_SOCKET_PATH" ]]; do
  kill -0 "$herdr_pid" 2>/dev/null || exit 1
  (( SECONDS < deadline )) || { printf 'herdr socket did not appear\n' >&2; exit 1; }
  sleep 0.1
done
exec "$SCRIPT_DIR/bin/bun" run "$SCRIPT_DIR/collie/bridge/index.ts"
LAUNCHER
chmod +x "$WORK_DIR/app/.sandbox/start.sh"
if [[ -n "${COLLIE_JOIN_TOKEN_FILE:-}" ]]; then
  cp -- "$COLLIE_JOIN_TOKEN_FILE" "$WORK_DIR/app/.sandbox/.join-token"
  chmod 600 "$WORK_DIR/app/.sandbox/.join-token"
fi

created=1
(cd "$WORK_DIR" && "$CF_BIN" push "$APP_NAME" --no-route --no-start -b "$BUILDPACK" -p app -c ./.sandbox/start.sh)
"$CF_BIN" set-env "$APP_NAME" COLLIE_PACK_TRANSPORT cf-identity >/dev/null 2>&1
"$CF_BIN" set-env "$APP_NAME" COLLIE_HOST 0.0.0.0 >/dev/null 2>&1
"$CF_BIN" set-env "$APP_NAME" SANDBOX_CWD "$SANDBOX_CWD" >/dev/null 2>&1
if [[ -n "${COLLIE_JOIN_TOKEN_FILE:-}" ]]; then
  "$CF_BIN" set-env "$APP_NAME" COLLIE_PACK_LEAD_ADDRESS "${COLLIE_PACK_LEAD_ADDRESS:?COLLIE_PACK_LEAD_ADDRESS is required with a join token}" >/dev/null 2>&1
  "$CF_BIN" set-env "$APP_NAME" COLLIE_JOIN_TOKEN_FILE /home/vcap/app/.sandbox/.join-token >/dev/null 2>&1
fi
"$CF_BIN" start "$APP_NAME"

printf '\nApp: %s\n' "$APP_NAME"
printf 'Inspect: cf logs %s\n' "$APP_NAME"
printf 'Inspect: cf app %s\n' "$APP_NAME"
printf 'Inspect: cf ssh %s\n' "$APP_NAME"
printf 'Cleanup: cf delete %s -f -r\n' "$APP_NAME"
if [[ "${SANDBOX_FOLLOW_LOGS:-}" == 1 ]]; then "$CF_BIN" logs "$APP_NAME"; fi
