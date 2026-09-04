#!/usr/bin/env bash
set -euo pipefail

if [[ ${SMOKE_LIVE:-0} != 1 ]]; then
  printf 'smoke: live execution deferred; set SMOKE_LIVE=1 only on the approved identity-routing CF\n' >&2
  exit 2
fi

for command in curl cf jq; do
  command -v "$command" >/dev/null 2>&1 || { printf 'smoke: required command not found: %s\n' "$command" >&2; exit 2; }
done
for variable in MANAGER_URL MANAGER_API_TOKEN MANAGER_APP_NAME MANAGER_APP_GUID MANAGER_ROUTE_HOST WRONG_IDENTITY_APP SMOKE_REPOSITORY SMOKE_BUILDPACK IDENTITY_DOMAIN; do
  [[ -n ${!variable:-} ]] || { printf 'smoke: %s is required\n' "$variable" >&2; exit 2; }
done

SMOKE_TIMEOUT=${SMOKE_TIMEOUT:-1200}
SMOKE_POLL_INTERVAL=${SMOKE_POLL_INTERVAL:-5}
SMOKE_WORKSPACE_CWD=${SMOKE_WORKSPACE_CWD:-/home/vcap/app}

early_cleanup() {
  local exit_status=$? base=${MANAGER_URL%/} guid=
  trap - EXIT INT TERM
  if [[ -n ${COOKIE_JAR:-} && -f ${COOKIE_JAR:-} ]]; then
    curl --silent --output /dev/null --request DELETE --cookie "$COOKIE_JAR" "$base/manager/api/sandboxes/$SANDBOX_NAME" || true
  else
    curl --silent --output /dev/null --request DELETE "$base/manager/api/sandboxes/$SANDBOX_NAME" || true
  fi
  guid=$(cf curl "/v3/apps?names=$SANDBOX_NAME" 2>/dev/null | jq -r '.resources[0].guid//empty' 2>/dev/null) || true
  cf remove-route-policy "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" --source "cf:app:$MANAGER_APP_GUID" >/dev/null 2>&1 || true
  [[ -z $guid ]] || cf remove-route-policy "$IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$guid" >/dev/null 2>&1 || true
  cf unmap-route "$SANDBOX_NAME" "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" >/dev/null 2>&1 || true
  cf delete-route "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" -f >/dev/null 2>&1 || true
  cf delete "$SANDBOX_NAME" -f -r >/dev/null 2>&1 || true
  [[ -z ${WORK_DIR:-} ]] || rm -rf "$WORK_DIR"
  exit "$exit_status"
}

SANDBOX_NAME=${SMOKE_NAME:-smoke-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM}
trap early_cleanup EXIT INT TERM
[[ $SANDBOX_NAME =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || { printf 'smoke: invalid sandbox name\n' >&2; exit 2; }
cleanup_armed=1
[[ $IDENTITY_DOMAIN =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && $IDENTITY_DOMAIN == *.* ]] || { printf 'smoke: invalid identity domain\n' >&2; exit 2; }
[[ $SMOKE_TIMEOUT =~ ^[1-9][0-9]*$ && $SMOKE_POLL_INTERVAL =~ ^[0-9]+$ ]] || { printf 'smoke: invalid timeout or poll interval\n' >&2; exit 2; }

MANAGER_URL=${MANAGER_URL%/}
IDENTITY_HOST="$SANDBOX_NAME.$IDENTITY_DOMAIN"
IDENTITY_URL="https://$IDENTITY_HOST"
WORK_DIR=$(mktemp -d)
chmod 700 "$WORK_DIR"
TOKEN_JSON="$WORK_DIR/login.json"
COOKIE_JAR="$WORK_DIR/cookies"
CREATE_JSON="$WORK_DIR/create.json"
WORKSPACE_JSON="$WORK_DIR/workspace.json"
RESPONSE_JSON="$WORK_DIR/response.json"
READY_JSON="$WORK_DIR/ready.json"
touch "$COOKIE_JAR" "$RESPONSE_JSON" "$READY_JSON"
printf '%s' "$MANAGER_API_TOKEN" | jq -Rs '{token:.}' >"$TOKEN_JSON"
jq -n --arg name "$SANDBOX_NAME" --arg repository "$SMOKE_REPOSITORY" --arg buildpack "$SMOKE_BUILDPACK" \
  '{name:$name,repository:$repository,buildpack:$buildpack}' >"$CREATE_JSON"
jq -n --arg cwd "$SMOKE_WORKSPACE_CWD" '{cwd:$cwd,label:"smoke"}' >"$WORKSPACE_JSON"
chmod 600 "$TOKEN_JSON" "$COOKIE_JAR" "$CREATE_JSON" "$WORKSPACE_JSON" "$RESPONSE_JSON" "$READY_JSON"

deleted=0
member_id=
create_submitted=
ready_observed=
delete_submitted=
absent_observed=
declare -A phase_observed=()

gateway_status() {
  local method=$1 url=$2 data_file=${3:-} status
  local args=(--silent --show-error --output "$RESPONSE_JSON" --write-out '%{http_code}' --request "$method" --cookie "$COOKIE_JAR" --cookie-jar "$COOKIE_JAR")
  [[ -z $data_file ]] || args+=(--header 'Content-Type: application/json' --data-binary "@$data_file")
  status=$(curl "${args[@]}" "$url") || true
  printf '%s' "$status"
}

plain_status() {
  local url=$1 status
  status=$(curl --silent --show-error --output "$RESPONSE_JSON" --write-out '%{http_code}' "$url") || true
  printf '%s' "$status"
}

valid_json() {
  jq -e . "$1" >/dev/null 2>&1 || { printf 'smoke: invalid manager JSON\n' >&2; return 1; }
}

api_error() {
  local status=$1
  printf 'smoke: manager request failed (HTTP %s): %s\n' "$status" \
    "$(jq -r 'if type=="object" then (.error//.message//"request failed") else "request failed" end | gsub("(?i)(bearer|token|password|secret)[=: ]+[^ ]+";"[redacted]")' "$RESPONSE_JSON" 2>/dev/null || printf 'request failed')" >&2
}

policy_json() {
  local output
  if ! output=$(cf curl "/routing/v1/route_policies" 2>/dev/null); then
    printf 'smoke: route-policy query failed\n' >&2
    return 1
  fi
  jq -e . <<<"$output" >/dev/null 2>&1 || { printf 'smoke: route-policy query returned invalid JSON\n' >&2; return 1; }
  printf '%s' "$output"
}

direct_cleanup() {
  cf remove-route-policy "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" --source "cf:app:$MANAGER_APP_GUID" >/dev/null 2>&1 || true
  if [[ -n ${sandbox_guid:-} ]]; then
    cf remove-route-policy "$IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$sandbox_guid" >/dev/null 2>&1 || true
  fi
  cf unmap-route "$SANDBOX_NAME" "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" >/dev/null 2>&1 || true
  cf delete-route "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" -f >/dev/null 2>&1 || true
  cf delete "$SANDBOX_NAME" -f -r >/dev/null 2>&1 || true
}

manager_absent() {
  local status
  status=$(gateway_status GET "$MANAGER_URL/manager/api/sandboxes")
  [[ $status == 200 ]] && valid_json "$RESPONSE_JSON" && ! jq -e --arg name "$SANDBOX_NAME" '.[] | select(.name==$name)' "$RESPONSE_JSON" >/dev/null
}

cf_resources_absent() {
  local apps routes policies
  apps=$(cf curl "/v3/apps?names=$SANDBOX_NAME") || return 1
  routes=$(cf curl "/v3/routes?hosts=$SANDBOX_NAME") || return 1
  policies=$(policy_json) || return 1
  [[ $(jq -r '.resources|length' <<<"$apps") == 0 && $(jq -r '.resources|length' <<<"$routes") == 0 ]] || return 1
  ! matching_policy sandbox "$policies" && ! matching_policy manager "$policies"
}

matching_policy() {
  local direction=$1 body=$2 source destination
  if [[ $direction == sandbox ]]; then source=$MANAGER_APP_GUID; destination=$IDENTITY_HOST
  else [[ -n ${sandbox_guid:-} ]] || return 1; source=$sandbox_guid; destination="$MANAGER_ROUTE_HOST.$IDENTITY_DOMAIN"
  fi
  jq -e --arg source "$source" --arg destination "$destination" '
    (.policies//.resources//[])[]
    | select(
        (.source.type=="app" or .source.type=="cf-app")
        and (.source.value//.source.id)==$source
        and (.destination.host//.destination.route.host)==$destination
      )
  ' <<<"$body" >/dev/null
}

managed_cleanup() {
  local exit_status=$?
  trap - EXIT INT TERM
  if (( cleanup_armed )); then
    local status deadline
    status=$(gateway_status DELETE "$MANAGER_URL/manager/api/sandboxes/$SANDBOX_NAME")
    deadline=$((SECONDS + SMOKE_TIMEOUT))
    while [[ $status == 202 ]] && (( SECONDS < deadline )); do
      if manager_absent; then
        if cf_resources_absent; then deleted=1; else direct_cleanup; fi
        break
      fi
      sleep "$SMOKE_POLL_INTERVAL"
    done
    if (( ! deleted )); then
      direct_cleanup
      cf_resources_absent >/dev/null 2>&1 || true
    fi
  fi
  rm -rf "$WORK_DIR"
  exit "$exit_status"
}
trap managed_cleanup EXIT INT TERM

target=$(cf target)
for field in user org space; do
  value=$(printf '%s\n' "$target" | jq -Rrs --arg field "$field" 'split("\n") | map(select(test("^"+$field+":";"i"))) | first // "" | sub("^[^:]+:[ ]*";"")')
  [[ -n $value ]] || { printf 'smoke: cf target has no authenticated %s\n' "$field" >&2; exit 1; }
done
cf help route-policies >/dev/null 2>&1 || { printf 'smoke: cf route-policy commands unavailable\n' >&2; exit 1; }
domain_json=$(cf curl "/v3/domains?names=$IDENTITY_DOMAIN")
[[ $(jq -r '.resources|length' <<<"$domain_json") == 1 ]] || { printf 'smoke: identity domain not found or ambiguous\n' >&2; exit 1; }

app_guid() {
  local name=$1 body
  body=$(cf curl "/v3/apps?names=$name") || return 1
  jq -er --arg name "$name" '.resources | map(select(.name==$name)) | if length==1 then .[0].guid else empty end' <<<"$body"
}
actual_manager_guid=$(app_guid "$MANAGER_APP_NAME") || { printf 'smoke: manager app identity unavailable\n' >&2; exit 1; }
wrong_guid=$(app_guid "$WRONG_IDENTITY_APP") || { printf 'smoke: wrong-identity app unavailable\n' >&2; exit 1; }
[[ $actual_manager_guid == "$MANAGER_APP_GUID" ]] || { printf 'smoke: manager app GUID does not match MANAGER_APP_GUID\n' >&2; exit 1; }
[[ $wrong_guid != "$actual_manager_guid" ]] || { printf 'smoke: wrong-identity app must differ from manager\n' >&2; exit 1; }

status=$(gateway_status POST "$MANAGER_URL/manager/api/session" "$TOKEN_JSON")
[[ $status == 204 ]] || { api_error "$status"; exit 1; }
rm -f "$TOKEN_JSON"
status=$(gateway_status POST "$MANAGER_URL/manager/api/sandboxes" "$CREATE_JSON")
[[ $status == 202 ]] || { api_error "$status"; exit 1; }
valid_json "$RESPONSE_JSON"
create_submitted=$SECONDS

assert_public_api_safe() {
  if jq -e 'paths as $p | ($p[-1]|strings|ascii_downcase) as $k | select($k|test("appguid|internalhost|certificate|certpath|keypath|secret|token|password"))' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed a forbidden key\n' >&2; return 1
  fi
  if jq -e --arg domain "$IDENTITY_DOMAIN" '..|strings|select(endswith("."+$domain) or test("-----BEGIN |(?i)(bearer|token|password|secret)[=: ]+|(^|/)(cert|certificate|key)(/|$)|join[_ -]?token";"i"))' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed identity or secret material\n' >&2; return 1
  fi
}
assert_public_api_safe

deadline=$((SECONDS + SMOKE_TIMEOUT))
while (( SECONDS < deadline )); do
  status=$(gateway_status GET "$MANAGER_URL/manager/api/sandboxes")
  [[ $status == 200 ]] || { api_error "$status"; exit 1; }
  valid_json "$RESPONSE_JSON"
  assert_public_api_safe
  phase=$(jq -er --arg name "$SANDBOX_NAME" '.[]|select(.name==$name)|.phase' "$RESPONSE_JSON") || { printf 'smoke: sandbox record missing\n' >&2; exit 1; }
  [[ -n ${phase_observed[$phase]:-} ]] || phase_observed[$phase]=$SECONDS
  case $phase in
    ready)
      member_id=$(jq -r --arg name "$SANDBOX_NAME" '.[]|select(.name==$name)|.packMemberId//empty' "$RESPONSE_JSON")
      [[ -n $member_id ]] || { printf 'smoke: ready sandbox has no Pack member ID\n' >&2; exit 1; }
      ready_observed=$SECONDS
      cp "$RESPONSE_JSON" "$READY_JSON"
      break ;;
    failed)
      printf 'smoke: lifecycle failed: %s\n' "$(jq -r --arg name "$SANDBOX_NAME" '.[]|select(.name==$name)|.lastError//"unspecified error"' "$RESPONSE_JSON")" >&2
      exit 1 ;;
  esac
  sleep "$SMOKE_POLL_INTERVAL"
done
[[ -n $member_id ]] || { printf 'smoke: lifecycle deadline exceeded\n' >&2; exit 1; }

sandbox_guid=$(app_guid "$SANDBOX_NAME") || { printf 'smoke: sandbox app not found\n' >&2; exit 1; }
[[ $sandbox_guid != "$actual_manager_guid" && $sandbox_guid != "$wrong_guid" ]] || { printf 'smoke: sandbox app identity is not distinct\n' >&2; exit 1; }
routes_json=$(cf curl "/v3/apps/$sandbox_guid/routes")
if ! route_rows=$(jq -er '.resources | if type=="array" then . else error("resources") end | .[] | [.host,.relationships.domain.data.guid] | @tsv' <<<"$routes_json"); then
  printf 'smoke: invalid sandbox route JSON\n' >&2
  exit 1
fi
while IFS=$'\t' read -r host domain_guid; do
  [[ -n $host && -n $domain_guid ]] || { printf 'smoke: invalid sandbox route JSON\n' >&2; exit 1; }
  domain=$(cf curl "/v3/domains/$domain_guid" | jq -er '.name')
  [[ $host == "$SANDBOX_NAME" && $domain == "$IDENTITY_DOMAIN" ]] || { printf 'smoke: ordinary public sandbox route is mapped\n' >&2; exit 1; }
done <<<"$route_rows"

status=$(plain_status "$IDENTITY_URL/pack/v1/hello")
case $status in 2??) printf 'smoke: identity route accepted request without instance certificate\n' >&2; exit 1 ;; esac

remote_probe='code=$(curl --silent --output /dev/null --write-out "%{http_code}" --cert "$CF_INSTANCE_CERT" --key "$CF_INSTANCE_KEY" "'"$IDENTITY_URL"'/pack/v1/hello"); printf "%s\n" "$code"'
wrong_status=$(cf ssh "$WRONG_IDENTITY_APP" -c "$remote_probe" | jq -Rrs 'split("\n")|map(select(test("^[0-9]{3}$")))|last//""')
[[ $wrong_status == 403 ]] || { printf 'smoke: wrong identity expected HTTP 403, got %s\n' "${wrong_status:-no status}" >&2; exit 1; }

pack_probe='HERDR_PLUGIN_CONFIG_DIR=./data/collie-config HERDR_PLUGIN_STATE_DIR=./data/collie-state COLLIE_STATE_DIR=./data/collie-state HERDR_SOCKET_PATH=./data/collie-state/herdr.sock COLLIE_HOST=127.0.0.1 COLLIE_PORT=9191 ./sandbox/runtime/bin/collie pack status'
pack_status=$(cf ssh "$MANAGER_APP_NAME" -c "$pack_probe") || { printf 'smoke: authenticated Pack status failed\n' >&2; exit 1; }
if ! awk -v member="$member_id" '
  $0 ~ "(^|[[:space:]])" member "([[:space:]]|$)" {
    found=1
    if ($0 ~ /unreachable/) exit 1
    if ($0 ~ /reachable/) { ok=1; exit 0 }
    next
  }
  found && /unreachable/ { exit 1 }
  found && /reachable/ { ok=1; exit 0 }
  END { exit !ok }
' <<<"$pack_status"; then
  printf 'smoke: authenticated Pack status lacks reachable member\n' >&2
  exit 1
fi

status=$(gateway_status GET "$MANAGER_URL/collie/api/snapshot?host=$member_id")
[[ $status == 200 ]] && valid_json "$RESPONSE_JSON" || { api_error "$status"; exit 1; }
jq -e --arg member "$member_id" '.servers[]|select(.id==$member and .reachable==true)' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: merged snapshot lacks member\n' >&2; exit 1; }
status=$(gateway_status POST "$MANAGER_URL/collie/api/workspace?host=$member_id" "$WORKSPACE_JSON")
[[ $status == 200 ]] && valid_json "$RESPONSE_JSON" && jq -e '.ok==true and (.pane.paneId|type=="string" and length>0)' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: Collie workspace creation failed\n' >&2; exit 1; }
pane_id=$(jq -er '.pane.paneId' "$RESPONSE_JSON")
workspace_id=$(jq -er '.pane.workspaceId' "$RESPONSE_JSON")
encoded_pane=$(jq -rn --arg value "$pane_id" '$value|@uri')

deadline=$((SECONDS + SMOKE_TIMEOUT))
workspace_visible=0
while (( SECONDS < deadline )); do
  status=$(gateway_status GET "$MANAGER_URL/collie/api/snapshot?host=$member_id")
  [[ $status == 200 ]] && valid_json "$RESPONSE_JSON" || { api_error "$status"; exit 1; }
  if jq -e --arg member "$member_id" --arg workspace "$workspace_id" --arg pane "$pane_id" '
    any(.workspaces[]?; .workspaceId==$workspace and .host==$member)
    and any((.agents[]?,.shellPanes[]?); .paneId==$pane and .host==$member)
  ' "$RESPONSE_JSON" >/dev/null; then workspace_visible=1; break; fi
  sleep "$SMOKE_POLL_INTERVAL"
done
(( workspace_visible )) || { printf 'smoke: created Collie workspace did not appear\n' >&2; exit 1; }

ACTION_JSON="$WORK_DIR/action.json"
jq -n --arg text "printf 'smoke-ready\\n'" '{text:$text,submit:true}' >"$ACTION_JSON"
chmod 600 "$ACTION_JSON"
status=$(gateway_status POST "$MANAGER_URL/collie/api/pane/$encoded_pane/reply?host=$member_id" "$ACTION_JSON")
[[ $status == 200 ]] && valid_json "$RESPONSE_JSON" && jq -e '.ok==true' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: Collie pane action failed\n' >&2; exit 1; }

deadline=$((SECONDS + SMOKE_TIMEOUT))
marker_visible=0
while (( SECONDS < deadline )); do
  status=$(gateway_status GET "$MANAGER_URL/collie/api/pane/$encoded_pane?host=$member_id")
  [[ $status == 200 ]] && valid_json "$RESPONSE_JSON" || { api_error "$status"; exit 1; }
  if jq -e --arg pane "$pane_id" '.paneId==$pane and any(.text|split("\n")[]; .=="smoke-ready")' "$RESPONSE_JSON" >/dev/null; then marker_visible=1; break; fi
  sleep "$SMOKE_POLL_INTERVAL"
done
(( marker_visible )) || { printf 'smoke: pane action marker did not appear\n' >&2; exit 1; }

printf 'Timing\n'
printf '%-20s %s\n' metric seconds
for spec in 'clone:clone' 'upload:upload' 'stage:stage' 'start:start-app' 'policy:secure-route' 'Pack:trigger-enrollment'; do
  label=${spec%%:*}; operation=${spec#*:}
  duration=$(jq -r --arg name "$SANDBOX_NAME" --arg operation "$operation" '.[]|select(.name==$name)|[.operations[]?|select(.name==$operation and .success==true)][0].duration//empty' "$READY_JSON")
  if [[ -n $duration ]]; then printf '%-20s %.3f\n' "$label" "$(jq -n "$duration/1000000000")"; else printf '%-20s unavailable\n' "$label"; fi
done
printf '%-20s %s\n' ready "$((ready_observed-create_submitted))"

status=$(gateway_status DELETE "$MANAGER_URL/manager/api/sandboxes/$SANDBOX_NAME")
[[ $status == 202 ]] || { api_error "$status"; exit 1; }
delete_submitted=$SECONDS
deadline=$((SECONDS + SMOKE_TIMEOUT))
while (( SECONDS < deadline )); do manager_absent && { deleted=1; absent_observed=$SECONDS; break; }; sleep "$SMOKE_POLL_INTERVAL"; done
(( deleted )) || { printf 'smoke: deletion deadline exceeded\n' >&2; exit 1; }

status=$(gateway_status GET "$MANAGER_URL/collie/api/snapshot?host=$member_id")
[[ $status == 200 ]] && valid_json "$RESPONSE_JSON" || { api_error "$status"; exit 1; }
! jq -e --arg member "$member_id" '.servers[]?|select(.id==$member)' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: member remains in snapshot after deletion\n' >&2; exit 1; }
status=$(gateway_status GET "$MANAGER_URL/collie/api/pack")
[[ $status == 200 ]] && valid_json "$RESPONSE_JSON" || { api_error "$status"; exit 1; }
! jq -e --arg member "$member_id" '.members[]?|select(.id==$member)' "$RESPONSE_JSON" >/dev/null || { printf 'smoke: member remains in Pack after deletion\n' >&2; exit 1; }

apps=$(cf curl "/v3/apps?names=$SANDBOX_NAME") || { printf 'smoke: app query failed after deletion\n' >&2; exit 1; }
routes=$(cf curl "/v3/routes?hosts=$SANDBOX_NAME") || { printf 'smoke: route query failed after deletion\n' >&2; exit 1; }
policies=$(policy_json)
[[ $(jq -r '.resources|length' <<<"$apps") == 0 ]] || { printf 'smoke: app remains after deletion\n' >&2; exit 1; }
[[ $(jq -r '.resources|length' <<<"$routes") == 0 ]] || { printf 'smoke: route remains after deletion\n' >&2; exit 1; }
! matching_policy sandbox "$policies" || { printf 'smoke: sandbox route policy remains after deletion\n' >&2; exit 1; }
! matching_policy manager "$policies" || { printf 'smoke: manager enrollment policy remains after deletion\n' >&2; exit 1; }
printf '%-20s %s\n' delete "$((absent_observed-delete_submitted))"

cleanup_armed=0
trap - EXIT INT TERM
rm -rf "$WORK_DIR"
printf 'PASS %s\n' "$SANDBOX_NAME"
