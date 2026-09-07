#!/usr/bin/env bash
set -euo pipefail

if [[ ${SMOKE_LIVE:-0} != 1 ]]; then
  printf 'smoke: live execution deferred; set SMOKE_LIVE=1 only on the approved identity-routing CF\n' >&2
  exit 2
fi

for command in curl jq; do
  command -v "$command" >/dev/null 2>&1 || { printf 'smoke: required command not found: %s\n' "$command" >&2; exit 2; }
done
if [[ -z ${CF_BIN:-} ]]; then
  CF_BIN=$(command -v cf 2>/dev/null || true)
fi
if [[ -z $CF_BIN && -n ${LAB_PROFILE_BIN_DIR:-} && -x $LAB_PROFILE_BIN_DIR/cf ]]; then
  CF_BIN=$LAB_PROFILE_BIN_DIR/cf
elif [[ -z $CF_BIN && -n ${USER:-} && -x /etc/profiles/per-user/$USER/bin/cf ]]; then
  CF_BIN=/etc/profiles/per-user/$USER/bin/cf
elif [[ -z $CF_BIN && -n ${HOME:-} && -x $HOME/.nix-profile/bin/cf ]]; then
  CF_BIN=$HOME/.nix-profile/bin/cf
fi
[[ -n $CF_BIN ]] || { printf 'smoke: required CF CLI not found; expose cf in PATH or set CF_BIN to its executable path\n' >&2; exit 2; }
[[ -f $CF_BIN && -x $CF_BIN ]] || { printf 'smoke: CF_BIN must name an executable file: %s\n' "$CF_BIN" >&2; exit 2; }
for variable in MANAGER_URL MANAGER_API_TOKEN MANAGER_APP_NAME MANAGER_APP_GUID MANAGER_ROUTE_HOST WRONG_IDENTITY_APP SMOKE_REPOSITORY SMOKE_BUILDPACK IDENTITY_DOMAIN; do
  [[ -n ${!variable:-} ]] || { printf 'smoke: %s is required\n' "$variable" >&2; exit 2; }
done

[[ -z ${SMOKE_INSECURE_PUBLIC_TLS:-} || ${SMOKE_INSECURE_PUBLIC_TLS:-} == 1 ]] || { printf 'smoke: SMOKE_INSECURE_PUBLIC_TLS must be 1 or unset\n' >&2; exit 2; }
[[ -z ${SMOKE_PUBLIC_CA_CERT:-} || ${SMOKE_INSECURE_PUBLIC_TLS:-} != 1 ]] || { printf 'smoke: SMOKE_PUBLIC_CA_CERT conflicts with SMOKE_INSECURE_PUBLIC_TLS=1\n' >&2; exit 2; }
public_curl_args=()
if [[ -n ${SMOKE_PUBLIC_CA_CERT:-} ]]; then
  [[ -f $SMOKE_PUBLIC_CA_CERT && -r $SMOKE_PUBLIC_CA_CERT && ! -L $SMOKE_PUBLIC_CA_CERT ]] || { printf 'smoke: SMOKE_PUBLIC_CA_CERT must be a readable regular non-symlink file\n' >&2; exit 2; }
  public_curl_args=(--cacert "$SMOKE_PUBLIC_CA_CERT")
elif [[ ${SMOKE_INSECURE_PUBLIC_TLS:-} == 1 ]]; then
  public_curl_args=(--insecure)
  printf 'smoke: public-route TLS verification disabled for lab\n' >&2
fi

SMOKE_TIMEOUT=${SMOKE_TIMEOUT:-1200}
SMOKE_CLEANUP_TIMEOUT=${SMOKE_CLEANUP_TIMEOUT:-120}
SMOKE_POLL_INTERVAL=${SMOKE_POLL_INTERVAL:-5}
SMOKE_WORKSPACE_CWD=${SMOKE_WORKSPACE_CWD:-/home/vcap/app}

SANDBOX_NAME=${SMOKE_NAME:-smoke-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM}
[[ $SANDBOX_NAME =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || { printf 'smoke: invalid sandbox name\n' >&2; exit 2; }
cleanup_armed=0
[[ $IDENTITY_DOMAIN =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && $IDENTITY_DOMAIN == *.* ]] || { printf 'smoke: invalid identity domain\n' >&2; exit 2; }
[[ $SMOKE_TIMEOUT =~ ^[1-9][0-9]*$ && $SMOKE_POLL_INTERVAL =~ ^[0-9]+$ ]] || { printf 'smoke: invalid timeout or poll interval\n' >&2; exit 2; }
[[ $SMOKE_CLEANUP_TIMEOUT =~ ^[1-9][0-9]*$ ]] || { printf 'smoke: SMOKE_CLEANUP_TIMEOUT must be a positive integer\n' >&2; exit 2; }

MANAGER_URL=${MANAGER_URL%/}
IDENTITY_HOST="$SANDBOX_NAME.$IDENTITY_DOMAIN"
IDENTITY_URL="https://$IDENTITY_HOST"
WORK_DIR=$(mktemp -d)
chmod 700 "$WORK_DIR"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM
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
  local args=("${public_curl_args[@]}" --silent --show-error --output "$RESPONSE_JSON" --write-out '%{http_code}' --request "$method" --cookie "$COOKIE_JAR" --cookie-jar "$COOKIE_JAR")
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

valid_sandbox_collection() {
  jq -e '
    type=="array" and all(.[];
      type=="object"
      and ((keys_unsorted)-["name","repository","revision","buildpack","desired","phase","resumePhase","packMemberId","lastError","operations","createdAt","updatedAt"]|length)==0
      and (.name|type=="string" and length>0)
      and (.repository|type=="string")
      and (.buildpack|type=="string" and length>0)
      and (.desired=="present" or .desired=="deleted")
      and (.phase|type=="string" and length>0)
      and (.createdAt|type=="string" and length>0)
      and (.updatedAt|type=="string" and length>0)
      and (.revision==null or (.revision|type=="string"))
      and (.resumePhase==null or (.resumePhase|type=="string"))
      and (.packMemberId==null or (.packMemberId|type=="string"))
      and (.lastError==null or (.lastError|type=="string"))
      and (.operations==null or ((.operations|type=="array") and all(.operations[];
        type=="object"
        and ((keys_unsorted)-["name","summary","startedAt","duration","success","error"]|length)==0
        and (.name|type=="string")
        and (.startedAt|type=="string")
        and (.duration|type=="number")
        and (.success|type=="boolean")
        and (.summary==null or (.summary|type=="string"))
        and (.error==null or (.error|type=="string"))
      )))
    )
  ' "$1" >/dev/null 2>&1
}

api_error() {
  local status=$1
  printf 'smoke: manager request failed (HTTP %s): %s\n' "$status" \
    "$(jq -r 'if type=="object" then (.error//.message//"request failed") else "request failed" end | gsub("(?i)(bearer|token|password|secret)[=: ]+[^ ]+";"[redacted]")' "$RESPONSE_JSON" 2>/dev/null || printf 'request failed')" >&2
}

route_collection() {
  local path=$1 expected_host=$2 expected_domain_guid=$3 output
  output=$("$CF_BIN" curl "$path") || return 1
  jq -e --arg host "$expected_host" --arg domain "$expected_domain_guid" '
    type=="object" and (.resources|type=="array")
    and all(.resources[];
      (.guid|type=="string" and length>0)
      and .host==$host
      and .relationships.domain.data.guid==$domain
    )
  ' <<<"$output" >/dev/null 2>&1 || return 1
  printf '%s' "$output"
}

policy_json() {
  local route_guid=$1 source_guid=$2 encoded_source output
  encoded_source=$(jq -rn --arg value "cf:app:$source_guid" '$value|@uri')
  if ! output=$("$CF_BIN" curl "/v3/route_policies?route_guids=$route_guid&sources=$encoded_source" 2>/dev/null); then
    printf 'smoke: route-policy query failed\n' >&2
    return 1
  fi
  jq -e '
    type=="object" and (.resources|type=="array")
    and all(.resources[];
      (.source|type=="string" and length>0)
      and (.relationships.route.data.guid|type=="string" and length>0)
    )
  ' <<<"$output" >/dev/null 2>&1 || { printf 'smoke: route-policy query returned unexpected schema\n' >&2; return 1; }
  printf '%s' "$output"
}

direct_cleanup() {
  "$CF_BIN" remove-route-policy "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" --source "cf:app:$MANAGER_APP_GUID" >/dev/null 2>&1 || true
  if [[ -n ${sandbox_guid:-} ]]; then
    "$CF_BIN" remove-route-policy "$IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$sandbox_guid" >/dev/null 2>&1 || true
  fi
  "$CF_BIN" unmap-route "$SANDBOX_NAME" "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" >/dev/null 2>&1 || true
  "$CF_BIN" delete-route "$IDENTITY_DOMAIN" --hostname "$SANDBOX_NAME" -f >/dev/null 2>&1 || true
  "$CF_BIN" delete "$SANDBOX_NAME" -f -r >/dev/null 2>&1 || true
}

manager_absent() {
  local status
  status=$(gateway_status GET "$MANAGER_URL/manager/api/sandboxes")
  [[ $status == 200 ]] || return 1
  if ! valid_sandbox_collection "$RESPONSE_JSON"; then
    printf 'smoke: manager sandbox deletion response returned unexpected schema\n' >&2
    return 1
  fi
  ! jq -e --arg name "$SANDBOX_NAME" '.[] | select(.name==$name)' "$RESPONSE_JSON" >/dev/null
}

cf_resources_absent() {
  local apps routes sandbox_policies manager_policies
  apps=$("$CF_BIN" curl "/v3/apps?names=$SANDBOX_NAME") || return 1
  routes=$("$CF_BIN" curl "/v3/routes?hosts=$SANDBOX_NAME") || return 1
  [[ -n ${sandbox_route_guid:-} && -n ${manager_route_guid:-} && -n ${sandbox_guid:-} ]] || return 1
  sandbox_policies=$(policy_json "$sandbox_route_guid" "$MANAGER_APP_GUID") || return 1
  manager_policies=$(policy_json "$manager_route_guid" "$sandbox_guid") || return 1
  [[ $(jq -r '.resources|length' <<<"$apps") == 0 && $(jq -r '.resources|length' <<<"$routes") == 0 ]] || return 1
  ! matching_policy "$sandbox_policies" "$sandbox_route_guid" "$MANAGER_APP_GUID" && ! matching_policy "$manager_policies" "$manager_route_guid" "$sandbox_guid"
}

matching_policy() {
  local body=$1 route_guid=$2 source_guid=$3
  jq -e --arg source "cf:app:$source_guid" --arg route "$route_guid" '
    .resources[]
    | select(.source==$source and .relationships.route.data.guid==$route)
  ' <<<"$body" >/dev/null
}

managed_cleanup() {
  local exit_status=$?
  trap - EXIT INT TERM
  if (( cleanup_armed )); then
    local status deadline
    status=$(gateway_status DELETE "$MANAGER_URL/manager/api/sandboxes/$SANDBOX_NAME")
    direct_cleanup
    deadline=$((SECONDS + SMOKE_CLEANUP_TIMEOUT))
    while (( SECONDS < deadline )); do
      if manager_absent; then deleted=1; break; fi
      sleep "$SMOKE_POLL_INTERVAL"
    done
    if (( ! deleted )); then
      printf 'smoke: cleanup deadline exceeded; residual manager record: %s\n' "$SANDBOX_NAME" >&2
    fi
  fi
  rm -rf "$WORK_DIR"
  exit "$exit_status"
}
trap managed_cleanup EXIT INT TERM

target=$("$CF_BIN" target)
for field in user org space; do
  value=$(printf '%s\n' "$target" | jq -Rrs --arg field "$field" 'split("\n") | map(select(test("^"+$field+":";"i"))) | first // "" | sub("^[^:]+:[ ]*";"")')
  [[ -n $value ]] || { printf 'smoke: cf target has no authenticated %s\n' "$field" >&2; exit 1; }
done
domain_json=$("$CF_BIN" curl "/v3/domains?names=$IDENTITY_DOMAIN")
[[ $(jq -r '.resources|length' <<<"$domain_json") == 1 ]] || { printf 'smoke: identity domain not found or ambiguous\n' >&2; exit 1; }
identity_domain_guid=$(jq -er '.resources[0].guid' <<<"$domain_json") || { printf 'smoke: identity domain response invalid\n' >&2; exit 1; }
manager_routes=$(route_collection "/v3/routes?hosts=$MANAGER_ROUTE_HOST&domain_guids=$identity_domain_guid" "$MANAGER_ROUTE_HOST" "$identity_domain_guid") || { printf 'smoke: manager enrollment route response invalid\n' >&2; exit 1; }
[[ $(jq -r '.resources|length' <<<"$manager_routes") == 1 ]] || { printf 'smoke: manager enrollment route not found or ambiguous\n' >&2; exit 1; }
manager_route_guid=$(jq -er '.resources[0].guid' <<<"$manager_routes")

app_guid() {
  local name=$1 body
  body=$("$CF_BIN" curl "/v3/apps?names=$name") || return 1
  jq -er --arg name "$name" '.resources | map(select(.name==$name)) | if length==1 then .[0].guid else empty end' <<<"$body"
}
actual_manager_guid=$(app_guid "$MANAGER_APP_NAME") || { printf 'smoke: manager app identity unavailable\n' >&2; exit 1; }
wrong_guid=$(app_guid "$WRONG_IDENTITY_APP") || { printf 'smoke: wrong-identity app unavailable\n' >&2; exit 1; }
[[ $actual_manager_guid == "$MANAGER_APP_GUID" ]] || { printf 'smoke: manager app GUID does not match MANAGER_APP_GUID\n' >&2; exit 1; }
[[ $wrong_guid != "$actual_manager_guid" ]] || { printf 'smoke: wrong-identity app must differ from manager\n' >&2; exit 1; }

existing_apps=$("$CF_BIN" curl "/v3/apps?names=$SANDBOX_NAME") || { printf 'smoke: sandbox name preflight app query failed\n' >&2; exit 1; }
existing_routes=$("$CF_BIN" curl "/v3/routes?hosts=$SANDBOX_NAME&domain_guids=$identity_domain_guid") || { printf 'smoke: sandbox name preflight route query failed\n' >&2; exit 1; }
jq -e '.resources|type=="array"' <<<"$existing_apps" >/dev/null 2>&1 || { printf 'smoke: sandbox name preflight app response invalid\n' >&2; exit 1; }
jq -e '.resources|type=="array"' <<<"$existing_routes" >/dev/null 2>&1 || { printf 'smoke: sandbox name preflight route response invalid\n' >&2; exit 1; }
if [[ $(jq -r '.resources|length' <<<"$existing_apps") != 0 || $(jq -r '.resources|length' <<<"$existing_routes") != 0 ]]; then
  printf 'smoke: sandbox name already has CF resources\n' >&2
  exit 1
fi

status=$(gateway_status POST "$MANAGER_URL/manager/api/session" "$TOKEN_JSON")
[[ $status == 204 ]] || { api_error "$status"; exit 1; }
rm -f "$TOKEN_JSON"
status=$(gateway_status GET "$MANAGER_URL/manager/api/sandboxes")
[[ $status == 200 ]] || { api_error "$status"; exit 1; }
valid_sandbox_collection "$RESPONSE_JSON" || { printf 'smoke: manager sandbox preflight returned unexpected schema\n' >&2; exit 1; }
if jq -e --arg name "$SANDBOX_NAME" '.[]|select(.name==$name)' "$RESPONSE_JSON" >/dev/null; then
  printf 'smoke: sandbox name already has a manager record\n' >&2
  exit 1
fi
cleanup_armed=1
trap managed_cleanup EXIT INT TERM
status=$(gateway_status POST "$MANAGER_URL/manager/api/sandboxes" "$CREATE_JSON")
[[ $status == 202 ]] || { api_error "$status"; exit 1; }
valid_json "$RESPONSE_JSON"
create_submitted=$SECONDS

assert_public_api_safe() {
  if jq -e 'paths as $p | ($p[-1]|strings|ascii_downcase) as $k | select($k|test("appguid|internalhost|certificate|certpath|keypath|secret|token|password"))' "$RESPONSE_JSON" >/dev/null; then
    printf 'smoke: manager API exposed a forbidden key\n' >&2; return 1
  fi
  if jq -e --arg domain "$IDENTITY_DOMAIN" '..|strings|select(endswith("."+$domain) or test("-----BEGIN |(?i)(bearer|token|password|secret)[=: ]+\\S+|join[_ -]?token[=: ]+\\S+|(^|/)(cert|certificate|key)(/|$)";"i"))' "$RESPONSE_JSON" >/dev/null; then
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
routes_json=$("$CF_BIN" curl "/v3/apps/$sandbox_guid/routes")
if ! route_rows=$(jq -er '.resources | if type=="array" then . else error("resources") end | .[] | [.guid,.host,.relationships.domain.data.guid] | @tsv' <<<"$routes_json"); then
  printf 'smoke: invalid sandbox route JSON\n' >&2
  exit 1
fi
while IFS=$'\t' read -r route_guid host domain_guid; do
  [[ -n $route_guid && -n $host && -n $domain_guid ]] || { printf 'smoke: invalid sandbox route JSON\n' >&2; exit 1; }
  domain=$("$CF_BIN" curl "/v3/domains/$domain_guid" | jq -er '.name')
  [[ $host == "$SANDBOX_NAME" && $domain == "$IDENTITY_DOMAIN" ]] || { printf 'smoke: ordinary public sandbox route is mapped\n' >&2; exit 1; }
  sandbox_route_guid=$route_guid
done <<<"$route_rows"
[[ -n ${sandbox_route_guid:-} ]] || { printf 'smoke: sandbox identity route missing\n' >&2; exit 1; }

status=$(plain_status "$IDENTITY_URL/pack/v1/hello")
case $status in 2??) printf 'smoke: identity route accepted request without instance certificate\n' >&2; exit 1 ;; esac

remote_probe='code=$(curl --silent --output /dev/null --write-out "%{http_code}" --cert "$CF_INSTANCE_CERT" --key "$CF_INSTANCE_KEY" "'"$IDENTITY_URL"'/pack/v1/hello"); printf "%s\n" "$code"'
wrong_status=$("$CF_BIN" ssh "$WRONG_IDENTITY_APP" -c "$remote_probe" | jq -Rrs 'split("\n")|map(select(test("^[0-9]{3}$")))|last//""')
[[ $wrong_status == 403 ]] || { printf 'smoke: wrong identity expected HTTP 403, got %s\n' "${wrong_status:-no status}" >&2; exit 1; }

pack_probe='HERDR_PLUGIN_CONFIG_DIR=./data/collie-config HERDR_PLUGIN_STATE_DIR=./data/collie-state COLLIE_STATE_DIR=./data/collie-state HERDR_SOCKET_PATH=./data/collie-state/herdr.sock COLLIE_HOST=127.0.0.1 COLLIE_PORT=9191 ./sandbox/runtime/bin/collie pack status'
pack_status=$("$CF_BIN" ssh "$MANAGER_APP_NAME" -c "$pack_probe") || { printf 'smoke: authenticated Pack status failed\n' >&2; exit 1; }
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

apps=$("$CF_BIN" curl "/v3/apps?names=$SANDBOX_NAME") || { printf 'smoke: app query failed after deletion\n' >&2; exit 1; }
routes=$("$CF_BIN" curl "/v3/routes?hosts=$SANDBOX_NAME") || { printf 'smoke: route query failed after deletion\n' >&2; exit 1; }
sandbox_policies=$(policy_json "$sandbox_route_guid" "$MANAGER_APP_GUID")
manager_policies=$(policy_json "$manager_route_guid" "$sandbox_guid")
[[ $(jq -r '.resources|length' <<<"$apps") == 0 ]] || { printf 'smoke: app remains after deletion\n' >&2; exit 1; }
[[ $(jq -r '.resources|length' <<<"$routes") == 0 ]] || { printf 'smoke: route remains after deletion\n' >&2; exit 1; }
! matching_policy "$sandbox_policies" "$sandbox_route_guid" "$MANAGER_APP_GUID" || { printf 'smoke: sandbox route policy remains after deletion\n' >&2; exit 1; }
! matching_policy "$manager_policies" "$manager_route_guid" "$sandbox_guid" || { printf 'smoke: manager enrollment policy remains after deletion\n' >&2; exit 1; }
printf '%-20s %s\n' delete "$((absent_observed-delete_submitted))"

cleanup_armed=0
trap - EXIT INT TERM
rm -rf "$WORK_DIR"
printf 'PASS %s\n' "$SANDBOX_NAME"
