#!/usr/bin/env bash
set -euo pipefail

: "${MANAGER_APP_NAME:?error: MANAGER_APP_NAME is required; set it to the CF app name}"

resolve_tool() {
  local variable=$1 name=$2 path profile_user
  path=${!variable:-}
  if [[ -z "$path" ]]; then
    path=$(command -v "$name" 2>/dev/null || true)
  fi
  if [[ -z "$path" && -n "${LAB_PROFILE_BIN_DIR:-}" && -x "$LAB_PROFILE_BIN_DIR/$name" ]]; then
    path="$LAB_PROFILE_BIN_DIR/$name"
  fi
  profile_user=${USER:-}
  if [[ -z "$profile_user" ]]; then
    profile_user=$(id -un 2>/dev/null || true)
  fi
  if [[ -z "$path" && -n "$profile_user" && -x "/etc/profiles/per-user/$profile_user/bin/$name" ]]; then
    path="/etc/profiles/per-user/$profile_user/bin/$name"
  fi
  if [[ -z "$path" && -n "${HOME:-}" && -x "$HOME/.nix-profile/bin/$name" ]]; then
    path="$HOME/.nix-profile/bin/$name"
  fi
  if [[ -z "$path" ]]; then
    printf 'error: required tool %s was not found; install it, expose it in PATH or a Nix user profile, or set %s\n' "$name" "$variable" >&2
    exit 1
  fi
  if [[ ! -f "$path" || ! -x "$path" ]]; then
    printf 'error: %s must name an executable file: %s\n' "$variable" "$path" >&2
    exit 1
  fi
  printf '%s' "$path"
}

CF_BIN=$(resolve_tool CF_BIN cf)
JQ_BIN=$(resolve_tool JQ_BIN jq)

guid=$(
  "$CF_BIN" app "$MANAGER_APP_NAME" --guid 2>/dev/null
) || {
  printf 'error: failed to resolve the manager app GUID\n' >&2
  exit 1
}
guid=${guid//$'\n'/}
if [[ ! "$guid" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]]; then
  printf 'error: CF returned an invalid manager app GUID\n' >&2
  exit 1
fi

environment=$(
  "$CF_BIN" curl "/v3/apps/$guid/env" 2>/dev/null
) || {
  printf 'error: failed to fetch manager app environment\n' >&2
  exit 1
}

token=$(printf '%s' "$environment" | "$JQ_BIN" -er '
  (.environment_variables // {}) as $environment_variables
  | (($environment_variables | type) == "object" and ($environment_variables | has("MANAGER_API_TOKEN"))) as $current_present
  | (($environment_variables | type) == "object" and ($environment_variables.run | type) == "object" and ($environment_variables.run | has("MANAGER_API_TOKEN"))) as $legacy_present
  | $environment_variables.MANAGER_API_TOKEN as $current
  | $environment_variables.run.MANAGER_API_TOKEN as $legacy
  | if $current_present and $legacy_present and $current != $legacy then
      error("conflicting token values")
    elif $current_present then
      $current
    elif $legacy_present then
      $legacy
    else
      error("missing token")
    end
  | if type == "string" and length > 0 then . else error("invalid token") end
' 2>/dev/null) || {
  printf 'error: manager app environment did not contain a nonempty MANAGER_API_TOKEN\n' >&2
  exit 1
}

printf '%s' "$token"
