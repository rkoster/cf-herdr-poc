# CF Pack Identity Spike

Status: Deferred; public-route TLS friction observed

Date: 2026-09-04

Reason: No approved full lifecycle smoke has run, so route-policy and Pack enrollment behavior remains unverified end to end.

Observed public-route preflight: local curl rejected the public manager certificate with code 60 because the lab CA was not trusted locally. An explicit insecure diagnostic returned HTTP 204 from login. The smoke harness prefers `SMOKE_PUBLIC_CA_CERT=/path/to/lab-ca.pem`; `SMOKE_INSECURE_PUBLIC_TLS=1` is a visible lab-only fallback restricted to public `MANAGER_URL` calls. It never changes the no-certificate identity probe or requests made through `cf ssh`, which continue to verify TLS while presenting instance credentials where required.

## Commands

Run after targeting a disposable space with an identity-aware domain and building `dist/`:

```bash
export MANAGER_APP_NAME=cf-herdr-manager
export CF_IDENTITY_DOMAIN=apps.internal
export MANAGER_PACK_HOST=cf-herdr-manager-pack.apps.internal
export MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"
export MANAGER_APP_GUID="$(cf app "$MANAGER_APP_NAME" --guid)"
export SANDBOX_APP_NAME=cf-herdr-identity-spike
export SANDBOX_ROUTE_HOST="$SANDBOX_APP_NAME"
export SANDBOX_GUID="$(cf app "$SANDBOX_APP_NAME" --guid)"

cf create-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf map-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf create-route "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST"
cf map-route "$SANDBOX_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST"
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST" --source "cf:app:$MANAGER_APP_GUID"
cf add-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID"
cf routes
cf curl "/v3/apps/$MANAGER_APP_GUID/processes"
cf curl "/v3/apps/$SANDBOX_GUID/processes"
cf logs "$MANAGER_APP_NAME" --recent
cf logs "$SANDBOX_APP_NAME" --recent
cf remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST" --source "cf:app:$MANAGER_APP_GUID"
cf remove-route-policy "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" --source "cf:app:$SANDBOX_GUID"
cf unmap-route "$MANAGER_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"
cf unmap-route "$SANDBOX_APP_NAME" "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST"
cf delete-route "$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST" -f
cf delete-route "$CF_IDENTITY_DOMAIN" --hostname "$SANDBOX_ROUTE_HOST" -f
```

From the manager and sandbox containers, use their instance certificate and key at `/etc/cf-instance-credentials/instance.crt` and `/etc/cf-instance-credentials/instance.key` to request `/pack/v1/hello` and the sandbox enrollment endpoint. Repeat without a certificate and from an unrelated app identity.

The deployed process contract is asymmetric: manager Collie remains on its configured loopback port with `COLLIE_PACK_TRANSPORT=cf-identity`; sandbox Collie exports `COLLIE_PACK_TRANSPORT=cf-identity`, `COLLIE_HOST=0.0.0.0`, `COLLIE_ALLOW_NON_LOOPBACK_BIND=1`, and derives `COLLIE_PORT` from CF's runtime `PORT`. `sandbox-bootstrap` inherits those values, listens on that port for enrollment, and exits before Collie starts. Gorouter terminates TLS, so both backend listeners serve plain HTTP. CF identity mode must leave the sandbox peer browser disabled.

## Assertions

- `MANAGER_PACK_HOST` remains the exact FQDN in HTTP Host matching and enrollment URLs; CF CLI `--hostname` receives only `MANAGER_ROUTE_HOST`.
- Identity routes are default-deny: no certificate, an unrelated app, and a browser cannot reach Pack endpoints.
- The manager identity reaches the sandbox route, and the sandbox identity reaches the manager Pack route.
- The manager public route is the only ordinary public route; no sandbox has one.
- Enrollment uses `https://$MANAGER_PACK_HOST` without duplicating the identity domain.
- Existing Pack authentication remains required after route-policy admission.
- Sandbox teardown resolves the current app GUID before any name-based unmap or deletion. A replacement app is neither unmapped nor deleted; GUID-independent policy and stable-route cleanup may continue. Because lookup and mutation are separate CF CLI calls, this is a race-narrowing preflight under the POC's trusted same-operator boundary, not an atomic identity guarantee.

## Capture

Record CF CLI/API version, identity-domain configuration, Gorouter/backend protocol, exact route and policy commands, app GUIDs, certificate paths (not contents), HTTP status for each identity matrix case, route-policy propagation delay, TLS handshake and request timings, enrollment/Collie restart timing, relevant bounded logs, cleanup outcome, and temporary workarounds. Do not record credentials, tokens, private keys, certificates, or fabricated observations.
