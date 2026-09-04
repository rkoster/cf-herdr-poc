#!/usr/bin/env bash
set -euo pipefail

: "${MANAGER_APP_NAME:?MANAGER_APP_NAME is required}"
: "${PUBLIC_DOMAIN:?PUBLIC_DOMAIN is required}"
: "${MANAGER_PUBLIC_HOST:?MANAGER_PUBLIC_HOST is required}"
: "${CF_IDENTITY_DOMAIN:?CF_IDENTITY_DOMAIN is required}"
: "${MANAGER_PACK_HOST:?MANAGER_PACK_HOST is required}"
: "${SANDBOX_BUILDPACKS:?SANDBOX_BUILDPACKS is required}"
: "${MANAGER_API_TOKEN:?MANAGER_API_TOKEN is required}"

MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
if [[ -z "$MANAGER_ROUTE_HOST" || "$MANAGER_ROUTE_HOST" == "$MANAGER_PACK_HOST" || "$MANAGER_ROUTE_HOST" == *.* || "$MANAGER_ROUTE_HOST.$CF_IDENTITY_DOMAIN" != "$MANAGER_PACK_HOST" || ! "$MANAGER_ROUTE_HOST" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
	printf 'error: MANAGER_PACK_HOST must be a direct child FQDN of CF_IDENTITY_DOMAIN\n' >&2
	exit 1
fi

cf push "$MANAGER_APP_NAME" -f manifest.yml --no-route --no-start
MANAGER_APP_GUID="$(cf app "$MANAGER_APP_NAME" --guid)"
cf set-env "$MANAGER_APP_NAME" CF_IDENTITY_DOMAIN "$CF_IDENTITY_DOMAIN"
cf set-env "$MANAGER_APP_NAME" SANDBOX_BUILDPACKS "$SANDBOX_BUILDPACKS"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_NAME "$MANAGER_APP_NAME"
cf set-env "$MANAGER_APP_NAME" MANAGER_APP_GUID "$MANAGER_APP_GUID"
cf set-env "$MANAGER_APP_NAME" MANAGER_PACK_HOST "$MANAGER_PACK_HOST"
cf set-env "$MANAGER_APP_NAME" MANAGER_API_TOKEN "$MANAGER_API_TOKEN"
cf create-route "$(cf target | awk '/org:/ {print $2; exit}')" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf map-route "$MANAGER_APP_NAME" "$PUBLIC_DOMAIN" --hostname "$MANAGER_PUBLIC_HOST"
cf create-route "$(cf target | awk '/org:/ {print $2; exit}')" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf start "$MANAGER_APP_NAME"
