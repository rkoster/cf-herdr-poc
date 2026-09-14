# Interactive Architecture Diagram Design

## Purpose

Create a presentation-ready architecture page for Cloud Foundry Summit. It should help a mixed
technical audience understand a Cloud Foundry application developer's day-to-day experience with
coding agents, while exposing enough implementation detail to support first-principles discussion
about native Cloud Foundry support for this workload.

The page is a north-star narrative grounded in the working CF Herdr POC. It must clearly separate
what exists today from unresolved platform-design questions.

## Artifact

Deliver one standalone HTML file containing inline SVG, CSS, and JavaScript. It requires no build
step, external assets, fonts, or network access. The primary canvas is 16:9 for conference
projection, with responsive behavior for smaller screens and a static print/export view.

## Narrative Structure

Use a developer-journey-first composition with three progressive stages. Earlier stages remain
visible as later stages appear.

### Stage 1: Developer Journey

Show a stable, left-to-right workflow:

1. Choose the work: select an application and repository.
2. Start an agent: request an isolated workspace.
3. Steer from anywhere: observe, prompt, and review agent work through Collie.
4. Ship through Cloud Foundry: test and push the result using normal CF workflows.

This stage establishes the developer value before introducing platform internals.

### Stage 2: Under the Hood

Reveal the current implementation as horizontal Cloud Foundry layers beneath the corresponding
journey steps. From top to bottom, these are clients, ingress, applications and control plane, and
Diego.

The clients layer contains two equal entry points:

- A Collie browser for observing, prompting, and reviewing agents.
- A developer terminal showing the resulting `cf ssh` connection into Herdr in the sandbox.

The ingress layer spans the full diagram width and contains Gorouter and SSH Proxy. Connections do
not run visibly through the full depth of this layer. Instead, each path terminates at a colored
port above ingress and resumes from a matching colored port below ingress. Use short labels only
where color and endpoint placement are insufficient.

The applications and control-plane layer contains two nested app containers plus CF API:

- The Manager app contains the manager API and reconciler, lifecycle state, CF CLI adapter, and
  lead Collie on loopback.
- The Sandbox app contains `sandbox-bootstrap`, sandbox Collie, Herdr, OpenCode and developer tools,
  CF CLI, and local sandbox state.
- CF API sits beside the app containers at the same visual level. It is behind Gorouter but outside
  Diego. Its only emphasized connection is the manager's lifecycle-control path.

The Diego layer spans the full width at the bottom and visually supports the Manager and Sandbox
app containers. CF API is not placed inside this layer.

The resulting architecture communicates that:

- An authenticated browser reaches one public route on the manager application.
- The manager serves its management UI and API and proxies the lead Collie on loopback.
- The manager reconciler invokes the CF CLI and records lifecycle state plus sanitized operation
  evidence.
- Cloud Foundry stages and runs short-lived sandbox applications.
- Each sandbox contains Collie, Herdr, Bun, OpenCode integration, and the CF CLI.
- Sandboxes have no ordinary public route.
- Each sandbox gets a default-deny identity route. Exact route policies permit required Pack
  traffic between the manager and that sandbox using Cloud Foundry instance identity.
- During startup, `sandbox-bootstrap` enrolls the sandbox into the manager's lead Collie Pack and
  exits before sandbox Collie binds the application port.
- Sandbox Collie communicates locally with Herdr. Browsers never connect directly to a sandbox or
  to Herdr.
- A developer can use `cf ssh` and Herdr directly in the sandbox as an alternative to Collie. The
  diagram shows only the resulting terminal-to-SSH-Proxy-to-sandbox connection and deliberately
  omits the CF SSH handshake and control details.
- Developer changes return through normal Cloud Foundry workflows, not through a hidden Pack
  software-distribution channel.

Color-match the paired connection segments on either side of ingress:

- Cyan: Collie browser through Gorouter to the Manager app and lead Collie.
- Green: developer terminal through SSH Proxy to Herdr in the Sandbox app.
- Blue: Manager app through Gorouter to CF API for lifecycle operations.
- Amber: lead Collie through the Gorouter identity route to sandbox Collie for Pack traffic.
- Purple: sandbox Collie to Herdr over the local socket, entirely inside the Sandbox app.
- White dashed: the developer's normal CF delivery path.

Label flows concisely so the audience can distinguish browser traffic, resulting SSH access,
manager control operations, Pack traffic, local Collie-to-Herdr traffic, and delivery without
overloading the ingress layer.

### Stage 3: First-Principles Questions

Reveal clickable discussion hotspots without presenting speculative answers as implemented design:

- Workspace primitive: should an agent workspace be modeled as an application?
- Identity and authorization: who or what may act on which application resources?
- Isolation and tenancy: what boundary is appropriate for untrusted agent execution?
- State and lifecycle: what should survive restarts, restaging, and pushes?
- Connectivity: which routes and protocols should agent control require?
- Observability and audit: how should operators and developers inspect agent actions?
- Resource governance: how should quota, duration, scaling, and cleanup work?

Each hotspot explains the current POC choice and frames the corresponding Cloud Foundry design
question.

## Visual System

Use a dark, high-contrast conference-slide aesthetic rather than an application dashboard.

- Blue denotes existing Cloud Foundry capabilities.
- Amber denotes CF Herdr POC components and behavior.
- Coral denotes unresolved north-star seams and design questions.
- A persistent legend defines these categories.
- Trust and routing boundaries are spatial regions, not merely labels.
- Primary labels and flow names must be readable on a projector; implementation details belong in
  tooltips and pinned notes.
- Use consistent line styles and arrow directions for each traffic class.

The stable upper portion presents the four-step developer journey. The lower portion expands into
full-width client, ingress, applications/control-plane, and Diego layers as stages advance. App
containers group their internal processes and tools; platform layers remain visually distinct from
those application boundaries.

## Interaction

- Provide visible controls for stages 1 through 3.
- Support `ArrowLeft`, `ArrowRight`, `Space`, and number keys `1` through `3`.
- Progressive transitions add content without removing prior narrative context.
- Hovering or focusing an interactive SVG element shows a concise tooltip.
- Clicking or activating an element pins an expanded speaker-note panel.
- Touch interaction uses tap for the same behavior.
- A reset control returns to stage 1 and clears pinned notes.
- Respect `prefers-reduced-motion` by removing nonessential animation.
- All controls and interactive diagram nodes are keyboard reachable and have meaningful accessible
  names.

## Tooltip Content

Every technical component and discussion hotspot has two levels of explanation:

- A short tooltip describing its role in plain language.
- A pinned detail describing whether it is implemented now, a current CF facility, or a north-star
  question, plus relevant protocol or trust-boundary context.

Tooltips must not expose secret values, identity material, internal sandbox route addresses, or
other deployment-specific sensitive data.

## Error And Fallback Behavior

The complete architecture remains understandable if JavaScript is disabled: all components are
rendered, and the page includes a brief static explanation of the three layers. JavaScript only
adds staged visibility, tooltips, pinned notes, and keyboard presentation controls.

Unknown keyboard input does nothing. Tooltip placement stays within the viewport. Missing optional
browser APIs must not prevent the diagram from rendering.

## Verification

Verify the artifact by:

- Opening it directly from disk and through a simple HTTP server.
- Checking all three stage controls and keyboard shortcuts.
- Checking hover, focus, click, and touch-equivalent tooltip behavior.
- Checking reset behavior and pinned speaker notes.
- Checking the complete static view with JavaScript disabled.
- Checking reduced-motion behavior.
- Checking 16:9 projector dimensions and representative desktop and mobile viewports.
- Checking print preview for a complete static architecture frame.
- Confirming architecture labels against the current README, manager reconciler, HTTP proxy, CF
  provider behavior, sandbox launcher, and Collie Pack architecture.

## Scope Boundaries

This artifact explains the current project and frames future design discussion. It does not propose
a production architecture, claim that unresolved platform primitives exist, add runtime behavior,
or become product documentation for every Collie subsystem.
