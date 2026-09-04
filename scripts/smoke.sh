#!/usr/bin/env bash
set -euo pipefail

if [[ ${SMOKE_LIVE:-0} != 1 ]]; then
  printf 'smoke: live execution deferred; set SMOKE_LIVE=1 only on the identity-routing CF with portable runtime binaries\n' >&2
  exit 2
fi

for command in curl cf jq; do
  command -v "$command" >/dev/null 2>&1 || { printf 'smoke: required command not found: %s\n' "$command" >&2; exit 2; }
done
for variable in MANAGER_URL MANAGER_API_TOKEN SMOKE_REPOSITORY SMOKE_BUILDPACK IDENTITY_DOMAIN MANAGER_APP_GUID; do
  [[ -n ${!variable:-} ]] || { printf 'smoke: %s is required\n' "$variable" >&2; exit 2; }
done

SMOKE_TIMEOUT=${SMOKE_TIMEOUT:-1200}
SMOKE_POLL_INTERVAL=${SMOKE_POLL_INTERVAL:-5}
if [[ -n ${SMOKE_NAME:-} ]]; then
  SANDBOX_NAME=$SMOKE_NAME
else
  SANDBOX_NAME="smoke-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
fi
[[ $SANDBOX_NAME =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || { printf 'smoke: invalid generated sandbox name\n' >&2; exit 2; }

MANAGER_URL=${MANAGER_URL%/}
IDENTITY_URL="https://${SANDBOX_NAME}.${IDENTITY_DOMAIN}"
WORK_DIR=$(mktemp -d)
chmod 700 "$WORK_DIR"
TOKEN_JSON="$WORK_DIR/login.json"
COOKIE_JAR="$WORK_DIR/cookies"
REQUEST_JSON="$WORK_DIR/create.json"
RESPONSE_JSON="$WORK_DIR/response.json"
READY_JSON="$WORK_DIR/ready.json"
printf '%s' "$MANAGER_API_TOKEN" | jq -Rs '{token:.}' >"$TOKEN_JSON"
jq -n --arg name "$SANDBOX_NAME" --arg repository "$SMOKE_REPOSITORY" --arg buildpack "$SMOKE_BUILDPACK" \
  '{name:$name,repository:$repository,buildpack:$buildpack}' >"$REQUEST_JSON"
touch "$COOKIE_JAR" "$RESPONSE_JSON" "$READY_JSON"
chmod 600 "$TOKEN_JSON" "$COOKIE_JAR" "$REQUEST_JSON" "$RESPONSE_JSON" "$READY_JSON"

created=0
deleted=0
member_id=

curl_status() {
  local method=$1 url=$2 data_file=${3:-} status
  local args=(--silent --show-error --output "$RESPONSE_JSON" --write-out '%{http_code}' --request "$method" --cookie "$COOKIE_JAR" --cookie-jar "$COOKIE_JAR")
  if [[ -n $data_file ]]; then args+=(--header 'Content-Type: application/json' --data-binary "@$data_file"); fi
  status=$(curl "${args[@]}" "$url") || true
  printf '%s' "$status"
}

api_error() {
  local status=$1
  printf 'smoke: manager request failed (HTTP %s): %s\n' "$status" \
    "$(jq -r '
      if type == "object" then (.error // .message // "request failed") else "request failed" end
      | gsub("-----BEGIN [^-]+-----.*"; "[redacted]")
      | gsub("(?i)(bearer|token|password|secret)[=: ]+[^ ]+"; "[redacted]")
    ' "$RESPONSE_JSON" 2>/dev/null || printf 'request failed')" >&2
}

direct_cleanup() {
  cf remove-route-policy "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" --source "cf:app:$MANAGER_APP_GUID" >/dev/null 2>&1 || true
  cf unmap-route "$SANDBOX_NAME" "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" >/dev/null 2>&1 || true
  cf delete-route "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" -f >/dev/null 2>&1 || true
  cf delete "$SANDBOX_NAME" -f -r >/dev/null 2>&1 || true
}

manager_absent() {
  local status
  status=$(curl_status GET "$MANAGER_URL/manager/api/sandboxes")
  [[ $status == 200 ]] && ! jq -e --arg name "$SANDBOX_NAME" '.[] | select(.name == $name)' "$RESPONSE_JSON" >/dev/null
}

cleanup() {
  local exit_status=$?
  trap - EXIT INT TERM
  if (( created )); then
    local status deadline
    status=$(curl_status DELETE "$MANAGER_URL/manager/api/sandboxes/$SANDBOX_NAME")
    deadline=$((SECONDS + 60))
    while [[ $status == 202 ]] && (( SECONDS < deadline )); do
      if manager_absent; then deleted=1; break; fi
      sleep "$SMOKE_POLL_INTERVAL"
    done
    (( deleted )) || direct_cleanup
  fi
  rm -rf "$WORK_DIR"
  exit "$exit_status"
}
trap cleanup EXIT INT TERM

status=$(curl_status POST "$MANAGER_URL/manager/api/session" "$TOKEN_JSON")
if [[ $status != 204 ]]; then api_error "$status"; exit 1; fi
rm -f "$TOKEN_JSON"

status=$(curl_status POST "$MANAGER_URL/manager/api/sandboxes" "$REQUEST_JSON")
if [[ $status != 202 ]]; then api_error "$status"; exit 1; fi
created=1

assert_public_api_safe() {
  if jq -e '
    paths as $p
    | (($p[-1] | strings | ascii_downcase) as $key
      | select($key | test("appguid|internalhost|certificate|certpath|keypath|secret|token|password")))
  ' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed a forbidden key\n' >&2
    return 1
  fi
  if jq -e --arg domain "$IDENTITY_DOMAIN" '.. | strings | select(endswith("." + $domain))' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed an internal identity hostname\n' >&2
    return 1
  fi
  if jq -e '.. | strings | select(test("-----BEGIN |(^|/)(cert|certificate|key)(/|$)|join[_ -]?token"; "i"))' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed certificate, key, or secret material\n' >&2
    return 1
  fi
}

assert_public_api_safe
deadline=$((SECONDS + SMOKE_TIMEOUT))
while (( SECONDS < deadline )); do
  status=$(curl_status GET "$MANAGER_URL/manager/api/sandboxes")
  if [[ $status != 200 ]]; then api_error "$status"; exit 1; fi
  assert_public_api_safe
  phase=$(jq -r --arg name "$SANDBOX_NAME" '.[] | select(.name == $name) | .phase' "$RESPONSE_JSON")
  case "$phase" in
    ready)
      member_id=$(jq -r --arg name "$SANDBOX_NAME" '.[] | select(.name == $name) | .packMemberId // empty' "$RESPONSE_JSON")
      [[ -n $member_id ]] || { printf 'smoke: ready sandbox has no Pack member ID\n' >&2; exit 1; }
      cp "$RESPONSE_JSON" "$READY_JSON"
      break
      ;;
    failed)
      printf 'smoke: lifecycle failed: %s\n' "$(jq -r --arg name "$SANDBOX_NAME" '.[] | select(.name == $name) | .lastError // "unspecified error"' "$RESPONSE_JSON")" >&2
      exit 1
      ;;
  esac
  sleep "$SMOKE_POLL_INTERVAL"
done
[[ -n $member_id ]] || { printf 'smoke: lifecycle deadline exceeded\n' >&2; exit 1; }

app_json=$(cf curl "/v3/apps?names=$SANDBOX_NAME")
app_guid=$(jq -r '.resources[0].guid // empty' <<<"$app_json")
[[ -n $app_guid ]] || { printf 'smoke: sandbox app not found\n' >&2; exit 1; }
routes_json=$(cf curl "/v3/apps/$app_guid/routes")
while IFS=$'\t' read -r host domain_guid; do
  [[ -n $domain_guid ]] || continue
  domain=$(cf curl "/v3/domains/$domain_guid" | jq -r '.name')
  [[ $host == "$SANDBOX_NAME" && $domain == "$IDENTITY_DOMAIN" ]] || { printf 'smoke: ordinary public sandbox route is mapped\n' >&2; exit 1; }
done < <(jq -r '.resources[] | [.host, .relationships.domain.data.guid] | @tsv' <<<"$routes_json")

status=$(curl_status GET "$IDENTITY_URL/pack/v1/hello")
case "$status" in 2??) printf 'smoke: identity route accepted a request without an instance certificate\n' >&2; exit 1 ;; esac

if [[ ${SMOKE_SKIP_WRONG_IDENTITY:-0} == 1 ]]; then
  printf 'SKIP wrong app identity (SMOKE_SKIP_WRONG_IDENTITY=1)\n'
elif [[ -n ${WRONG_IDENTITY_PROBE_CMD:-} ]]; then
  SMOKE_IDENTITY_URL="$IDENTITY_URL" "$WRONG_IDENTITY_PROBE_CMD"
else
  printf 'smoke: WRONG_IDENTITY_PROBE_CMD is required (or explicitly set SMOKE_SKIP_WRONG_IDENTITY=1)\n' >&2
  exit 1
fi

status=$(curl_status GET "$MANAGER_URL/collie/api/snapshot?sessions=all")
[[ $status == 200 ]] || { api_error "$status"; exit 1; }
jq -e --arg member "$member_id" '.servers[] | select(.id == $member and .reachable == true and .protocol == "ok")' "$RESPONSE_JSON" >/dev/null || {
  printf 'smoke: Pack snapshot lacks a reachable sandbox member\n' >&2; exit 1;
}
status=$(curl_status GET "$MANAGER_URL/collie/api/pack")
[[ $status == 200 ]] || { api_error "$status"; exit 1; }
jq -e --arg member "$member_id" '.members[] | select(.id == $member and .health == "reachable")' "$RESPONSE_JSON" >/dev/null || {
  printf 'smoke: Pack status lacks a reachable sandbox member\n' >&2; exit 1;
}

printf 'Timing (manager-reported)\n'
printf '%-24s %s\n' phase seconds
jq -r --arg name "$SANDBOX_NAME" '.[] | select(.name == $name) | .operations[]? | [.name, ((.duration / 1000000000) | tostring)] | @tsv' "$READY_JSON" 2>/dev/null || true

status=$(curl_status DELETE "$MANAGER_URL/manager/api/sandboxes/$SANDBOX_NAME")
[[ $status == 202 ]] || { api_error "$status"; exit 1; }
deadline=$((SECONDS + SMOKE_TIMEOUT))
while (( SECONDS < deadline )); do
  if manager_absent; then deleted=1; break; fi
  sleep "$SMOKE_POLL_INTERVAL"
done
(( deleted )) || { printf 'smoke: deletion deadline exceeded\n' >&2; exit 1; }
created=0

status=$(curl_status GET "$MANAGER_URL/collie/api/snapshot?sessions=all")
[[ $status == 200 ]] || { api_error "$status"; exit 1; }
! jq -e --arg member "$member_id" '.servers[]? | select(.id == $member)' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: Pack member remains after deletion\n' >&2; exit 1; }
[[ $(cf curl "/v3/apps?names=$SANDBOX_NAME" | jq '.resources | length') == 0 ]] || { printf 'smoke: app remains after deletion\n' >&2; exit 1; }
[[ $(cf curl "/v3/routes?hosts=$SANDBOX_NAME" | jq '.resources | length') == 0 ]] || { printf 'smoke: route remains after deletion\n' >&2; exit 1; }
policy_json=$(cf curl "/routing/v1/route_policies?host=$SANDBOX_NAME.$IDENTITY_DOMAIN" 2>/dev/null || printf '{"policies":[]}')
[[ $(jq '(.policies // .resources // []) | length' <<<"$policy_json") == 0 ]] || { printf 'smoke: route policy remains after deletion\n' >&2; exit 1; }

printf 'PASS %s\n' "$SANDBOX_NAME"
