# Cloud Foundry Herdr Sandbox Manager POC

## Purpose

Build a user-experience POC for creating disposable Cloud Foundry sandboxes that run Herdr,
Collie, and optionally OpenCode. The POC should make the workflow compelling while preserving
evidence of platform friction for the Agentic Workloads CF working group.

The POC may use expedient implementation choices. It must not hide staging delays, policy
propagation, retries, or cleanup failures that could inform future Cloud Foundry features.

## Chosen Approach

Add a small sandbox-manager application around an otherwise mostly unchanged lead Collie. Its API
and gateway are written in Go. The manager owns Cloud Foundry lifecycle operations, serves the
sandbox-management UI, and proxies the lead Collie UI and API. Every sandbox runs Herdr and a Collie
Pack peer. The peer's own UI is not publicly exposed.

This approach reuses Collie's existing Pack aggregation and action forwarding. It avoids teaching
central Collie to connect to remote Herdr sockets and avoids placing Cloud Foundry credentials and
provisioning logic inside Collie itself.

## Topology

The manager and lead Collie run in one Cloud Foundry app for the POC. They share one Diego app
identity and one public entry point.

Each sandbox is a separate Cloud Foundry app containing:

- Herdr
- A Collie instance configured as a Pack peer
- OpenCode when selected by the sandbox runtime configuration
- The repository supplied at creation time
- Dependencies supplied by the selected buildpack

Only the manager app has a public route. Each sandbox receives one route on an identity-aware
domain. That route exists solely for manager-to-peer Collie Pack traffic. Browser responses and
client-side state must never expose sandbox route addresses.

All browser reads and writes flow through the manager's public route, then through lead Collie and
the Pack protocol to the sandbox peer.

## Creation Inputs

The initial form accepts:

- A unique sandbox name
- A Git repository URL
- A buildpack selected from a manager-controlled allow-list

The allow-list keeps user input out of command positions when the POC shells out to the `cf` CLI.
Repository authentication, if needed for the demo, is manager configuration rather than arbitrary
form input.

## Components

### Sandbox Provider

A thin Cloud Foundry adapter creates, stages, starts, inspects, and deletes sandbox apps. For POC
speed it may invoke the `cf` CLI rather than integrate directly with Cloud Controller APIs. Every
invocation captures command, timing, exit status, and sanitized output as friction evidence.

### Sandbox Reconciler

The reconciler advances each sandbox through explicit desired and observed phases. Operations are
idempotent enough to retry after a manager restart or a partial failure. A failed operation leaves
the sandbox record visible with its last error and a retry action.

Creation phases are:

1. `creating`
2. `staging`
3. `starting`
4. `securing-route`
5. `joining-pack`
6. `ready`

Deletion phases reverse dependencies:

1. Mark the sandbox unavailable to new user actions.
2. Remove the peer from the Pack.
3. Remove route policies and the identity-aware route.
4. Delete the Cloud Foundry app.
5. Delete the manager record after cleanup succeeds.

A cleanup failure preserves the record and supports retry.

### Pack Provisioner

The provisioner generates the sandbox peer configuration and enrolls a healthy peer into the lead
Collie Pack. Enrollment begins only after the sandbox process is healthy and its identity-aware
route policy is usable. Existing Pack authentication remains enabled as defense in depth.

The provisioner removes Pack enrollment before sandbox route or app deletion. It records the Pack
member ID and internal route in server-side sandbox state.

### Web Gateway

The Go gateway owns the sole public route. It serves a sandbox-management frontend and
reverse-proxies the lead Collie UI and API under the same origin. The manager frontend uses the same
stack as Collie: React Router, Vite, TypeScript, Tailwind, and shadcn. It should reuse Collie's visual
patterns where practical, while remaining a separately built application for the initial POC.

The first version may present separate `Sandboxes` and `Agents` navigation surfaces. Embedding
sandbox controls directly into Collie's UI is a later nice-to-have, not required for the initial
POC.

The gateway and manager APIs enforce the same operator authentication boundary as lead Collie.

## Technology Choices

The manager backend is a Go service. It contains the HTTP API, Cloud Foundry provider, reconciler,
Pack provisioner, persistent state adapter, authentication integration, and reverse proxy. The
initial provider may execute the `cf` CLI from Go to prioritize POC speed and expose workflow
friction; its interface should permit later replacement with direct Cloud Controller API calls.

The manager frontend is a separate React Router and Vite TypeScript application using Tailwind and
shadcn, matching Collie's frontend stack. It consumes only the Go manager API. Sandbox peer routes,
CF credentials, Pack credentials, and instance identity material remain backend-only.

## Identity-Aware Routing

The target Cloud Foundry deployment provides identity-aware routing as documented by Cloud Foundry.
The sandbox manager performs these operations for every sandbox:

1. Create a route for the peer Collie on the identity-aware domain.
2. Map the route to the sandbox app.
3. Add a destination route policy whose source is `cf:app:<manager-app-guid>`.
4. Call the sandbox route from the manager using the short-lived certificate and key mounted at
   `CF_INSTANCE_CERT` and `CF_INSTANCE_KEY`.
5. Poll until the route accepts the manager and the peer reports healthy before Pack enrollment.

The identity-aware domain is default-deny. No browser or unrelated app identity receives a policy
allowing access to a sandbox route. Gorouter validates mTLS and pins authorization to the manager
app GUID through the route policy.

Sandbox apps need no route back to the manager for normal request/response Pack proxying. If live
Pack behavior proves that peer-initiated callbacks are mandatory, the manager may add a separate
identity-aware callback route restricted to enrolled sandbox app GUIDs. This exception must not
create a public route for any sandbox.

## Persistent State

For the POC, state may be stored in a JSON file on a mounted volume. Each sandbox record contains:

- Sandbox name
- Cloud Foundry app GUID
- Desired and observed phase
- Git repository and resolved revision when available
- Selected buildpack
- Server-side identity-aware route
- Pack member ID
- Creation and update timestamps
- Lifecycle operation timings
- Last sanitized operation error

Writes use atomic replacement to survive manager restarts. The reconciler compares persisted state
with Cloud Foundry and Pack state rather than assuming the last process completed its operation.

## User Experience

After submitting the form, the user sees live creation phases and elapsed time. Once the sandbox is
`ready`, its agents appear through the lead Collie Pack dashboard. Users interact with those agents
only through the manager and lead Collie; they never navigate to the sandbox peer.

The sandbox detail view shows staging duration, route-policy propagation time, retries, and failed
operations. These are first-class POC findings. Deletion similarly shows teardown progress and
retains failed cleanup for inspection and retry.

## Failure Handling

The manager uses bounded retries with visible errors for eventually consistent operations such as
app startup and policy propagation. It does not attempt distributed transactions. Each lifecycle
step checks whether its desired resource already exists before creating or deleting it.

Secrets, certificate keys, Pack credentials, repository credentials, and raw environment values are
excluded from logs and browser responses. A failed sandbox is not enrolled into the Pack. A sandbox
being deleted stops accepting newly proxied actions before teardown begins.

## Testing

Unit tests cover:

- Lifecycle phase ordering and retry behavior
- Idempotent reconciliation after partial creation and deletion
- Buildpack allow-list validation and safe command argument construction
- Route-policy creation pinned to the manager app GUID
- Pack enrollment only after health and route-policy readiness
- Removal from Pack before route and app deletion
- Redaction of secrets and omission of sandbox routes from browser responses

Go tests cover provider, reconciliation, persistence, proxy, and API behavior. Frontend tests use
the same testing conventions and tools as Collie where practical, with mocked manager API responses.

An integration smoke test against the target Cloud Foundry deployment will:

1. Create a sandbox from a test repository and buildpack.
2. Verify access to the sandbox route without a client certificate is denied.
3. Verify access from a different app identity without a policy is denied.
4. Verify the manager app identity can reach the peer.
5. Verify the peer appears in lead Collie and a Pack action reaches Herdr.
6. Delete the sandbox and verify its Pack member, policy, route, and app are removed.

## Non-Goals

- A production-grade multi-tenant control plane
- Hiding Cloud Foundry operational friction
- Direct central Collie connections to remote Herdr sockets
- Public routes or independently usable UIs for sandbox Collie peers
- Replacing Pack authentication with route identity
- Supporting arbitrary buildpack or shell arguments
- Perfect rollback across Cloud Foundry and Pack operations

## Success Criteria

A user can provide a name, repository, and buildpack; observe creation progress; use the resulting
Herdr-hosted agents through the manager's Collie UI; and delete the sandbox. Sandbox Collie peers
remain unreachable through public routes and accept Pack traffic only from the pinned manager app
identity. The demo captures enough timings and failures to identify concrete Cloud Foundry workflow
friction.
