# Interactive Architecture Diagram Revision Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Revise the interactive CF Herdr architecture page into a clear top-down Cloud Foundry topology with client, ingress, applications/control-plane, and Diego layers.

**Architecture:** Keep the existing standalone HTML, progressive stages, tooltips, and controls. Replace only the stage-two SVG topology and its supporting styles/content tests: clients sit at the top, ingress spans the width with paired colored ports, nested app containers and CF API occupy the middle, and Diego forms the bottom platform layer.

**Tech Stack:** HTML5, inline SVG, CSS, vanilla JavaScript, Go `testing`, Vitest, JSDOM

---

## File Structure

- Modify `docs/presentations/cf-herdr-architecture.html`: replace stage-two topology and update flow styles/tooltips.
- Modify `scripts/architecture_diagram_test.go`: enforce the revised layer, grouping, and connection contract.
- Keep `web/src/architecture-diagram.test.ts` unchanged unless renamed nodes require selector updates.
- Modify `docs/superpowers/specs/2026-09-14-interactive-architecture-diagram-design.md`: already revised and approved.

Do not commit unless the user explicitly requests it.

### Task 1: Lock The Revised Topology Contract

**Files:**
- Modify: `scripts/architecture_diagram_test.go`
- Test: `scripts/architecture_diagram_test.go`

- [ ] **Step 1: Replace the stage-two content expectations**

Update `TestArchitectureDiagramCoversApprovedStory` so its required strings include:

```go
		`id="clients-layer"`, `CLIENTS`, `Collie browser`, `Developer terminal`, `cf ssh`,
		`id="ingress-layer"`, `INGRESS`, `Gorouter`, `SSH Proxy`,
		`id="apps-layer"`, `APPLICATIONS + CONTROL PLANE`,
		`id="manager-app"`, `APP · CF HERDR MANAGER`, `Manager API + reconciler`,
		`Lifecycle state`, `CF CLI adapter`, `Lead Collie`,
		`id="sandbox-app"`, `APP · AGENT SANDBOX`, `sandbox-bootstrap`,
		`Sandbox Collie`, `Herdr`, `OpenCode + tools`, `Local sandbox state`,
		`id="cf-api"`, `CF API`, `Outside Diego`,
		`id="diego-layer"`, `DIEGO`, `Schedules and runs application containers`,
		`data-flow="browser"`, `data-flow="ssh"`, `data-flow="control"`,
		`data-flow="pack"`, `data-flow="socket"`, `data-flow="delivery"`,
```

Remove the obsolete combined expectation `Cloud Controller · Diego` and the old side-by-side foundation labels.

- [ ] **Step 2: Add structural exclusion checks**

Add this assertion to ensure the simplified SSH story and CF API placement remain focused:

```go
	for _, forbidden := range []string{
		`CF SSH handshake`,
		`Cloud Controller · Diego`,
		`DEVELOPER EDGE`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("diagram contains obsolete topology %q", forbidden)
		}
	}
```

- [ ] **Step 3: Run the test and verify the expected failure**

Run: `go test ./scripts -run ArchitectureDiagramCoversApprovedStory -v`

Expected: FAIL for missing layer IDs, nested app labels, paired flow attributes, and Diego layer.

### Task 2: Replace Stage Two With Horizontal Platform Layers

**Files:**
- Modify: `docs/presentations/cf-herdr-architecture.html`
- Test: `scripts/architecture_diagram_test.go`

- [ ] **Step 1: Replace the existing `stage-2` SVG group**

Use four top-down regions within the stage-two space:

```html
<g id="stage-2" data-stage="2">
  <g id="clients-layer">
    <text class="layer-title" x="80" y="420">CLIENTS</text>
    <g class="node client-node poc-node" tabindex="0" role="button"
       aria-label="Collie browser"
       data-title="Collie browser"
       data-short="Observe, prompt, and review agents through one web front door."
       data-detail="Working POC: the browser reaches the manager route and lead Collie; it never connects directly to a sandbox.">
      <rect x="250" y="392" width="270" height="62" rx="14"/>
      <text x="275" y="420">Collie browser</text>
      <text class="node-copy" x="275" y="442">Observe · prompt · review</text>
    </g>
    <g class="node client-node poc-node" tabindex="0" role="button"
       aria-label="Developer terminal using cf ssh"
       data-title="Developer terminal"
       data-short="Use cf ssh and Herdr directly inside the sandbox."
       data-detail="Current developer path: cf ssh yields a terminal connection through SSH Proxy to Herdr in the sandbox. Handshake details are intentionally omitted.">
      <rect x="1080" y="392" width="270" height="62" rx="14"/>
      <text x="1105" y="420">Developer terminal</text>
      <text class="node-copy" x="1105" y="442">cf ssh · Herdr</text>
    </g>
  </g>

  <g id="ingress-layer">
    <rect class="layer ingress-layer" x="70" y="480" width="1460" height="92" rx="18"/>
    <text class="layer-title" x="95" y="510">INGRESS</text>
    <text class="ingress-service" x="490" y="535">Gorouter</text>
    <text class="ingress-service" x="1180" y="535">SSH Proxy</text>
    <circle class="port browser-port" cx="385" cy="480" r="7"/>
    <circle class="port browser-port" cx="385" cy="572" r="7"/>
    <circle class="port ssh-port" cx="1215" cy="480" r="7"/>
    <circle class="port ssh-port" cx="1215" cy="572" r="7"/>
    <circle class="port control-port" cx="790" cy="480" r="7"/>
    <circle class="port control-port" cx="790" cy="572" r="7"/>
    <circle class="port pack-port" cx="930" cy="480" r="7"/>
    <circle class="port pack-port" cx="930" cy="572" r="7"/>
  </g>

  <g id="apps-layer">
    <text class="layer-title" x="80" y="610">APPLICATIONS + CONTROL PLANE</text>
    <g id="manager-app" class="app-container">
      <rect x="100" y="630" width="475" height="150" rx="18"/>
      <text class="app-label" x="125" y="656">APP · CF HERDR MANAGER</text>
      <g class="component"><rect x="125" y="674" width="195" height="40" rx="9"/><text x="140" y="700">Manager API + reconciler</text></g>
      <g class="component"><rect x="335" y="674" width="105" height="40" rx="9"/><text x="350" y="700">Lifecycle state</text></g>
      <g class="component"><rect x="455" y="674" width="95" height="40" rx="9"/><text x="470" y="700">CF CLI adapter</text></g>
      <g class="node poc-node" tabindex="0" role="button" aria-label="Lead Collie"
         data-title="Lead Collie" data-short="The managed web and Pack front door."
         data-detail="Working POC: lead Collie runs on loopback under manager supervision and connects to sandbox Collie through an identity route.">
        <rect x="125" y="728" width="425" height="36" rx="9"/><text x="140" y="753">Lead Collie · loopback</text>
      </g>
    </g>

    <g id="cf-api" class="node cf-node" tabindex="0" role="button" aria-label="CF API"
       data-title="CF API" data-short="Receives sandbox lifecycle operations from the manager."
       data-detail="Current CF control plane: the manager uses CF CLI-backed API operations to create, stage, start, inspect, and delete sandbox apps.">
      <rect x="620" y="650" width="220" height="105" rx="16"/>
      <text x="650" y="690">CF API</text>
      <text class="node-copy" x="650" y="720">Lifecycle control</text>
      <text class="node-copy" x="650" y="744">Outside Diego</text>
    </g>

    <g id="sandbox-app" class="app-container">
      <rect x="885" y="630" width="615" height="150" rx="18"/>
      <text class="app-label" x="910" y="656">APP · AGENT SANDBOX</text>
      <g class="component"><rect x="910" y="674" width="140" height="40" rx="9"/><text x="925" y="700">sandbox-bootstrap</text></g>
      <g class="node poc-node" tabindex="0" role="button" aria-label="Sandbox Collie"
         data-title="Sandbox Collie" data-short="Joins the lead Pack and bridges browser actions locally."
         data-detail="Working POC: sandbox Collie has no public browser UI and receives policy-gated Pack traffic through its identity route.">
        <rect x="1065" y="674" width="125" height="40" rx="9"/><text x="1080" y="700">Sandbox Collie</text>
      </g>
      <g class="node poc-node" tabindex="0" role="button" aria-label="Herdr"
         data-title="Herdr" data-short="Owns terminal panes, agents, and workspace state."
         data-detail="Working POC: Collie uses Herdr's local Unix socket; cf ssh gives the developer direct terminal access to the same sandbox.">
        <rect x="1205" y="674" width="90" height="40" rx="9"/><text x="1220" y="700">Herdr</text>
      </g>
      <g class="component"><rect x="1310" y="674" width="165" height="40" rx="9"/><text x="1325" y="700">OpenCode + tools</text></g>
      <g class="component"><rect x="910" y="728" width="140" height="36" rx="9"/><text x="925" y="753">CF CLI</text></g>
      <g class="component"><rect x="1065" y="728" width="230" height="36" rx="9"/><text x="1080" y="753">Local sandbox state</text></g>
    </g>
  </g>

  <g id="diego-layer">
    <rect class="layer diego-layer" x="70" y="805" width="1460" height="50" rx="16"/>
    <text class="layer-title" x="100" y="836">DIEGO</text>
    <text class="layer-copy" x="800" y="836" text-anchor="middle">Schedules and runs application containers</text>
  </g>

  <g class="flows">
    <path class="flow browser-flow" data-flow="browser" d="M385 454V480"/><path class="flow browser-flow" data-flow="browser" d="M385 572V630"/><text class="flow-label" x="400" y="476">HTTPS</text>
    <path class="flow ssh-flow" data-flow="ssh" d="M1215 454V480"/><path class="flow ssh-flow" data-flow="ssh" d="M1215 572V674"/><text class="flow-label" x="1230" y="476">SSH</text>
    <path class="flow control-flow" data-flow="control" d="M550 690C600 610 700 590 790 572"/><path class="flow control-flow" data-flow="control" d="M790 572V650"/><text class="flow-label" x="690" y="620">CF lifecycle</text>
    <path class="flow pack-flow" data-flow="pack" d="M500 746C620 610 760 590 930 572"/><path class="flow pack-flow" data-flow="pack" d="M930 572C980 620 1040 650 1128 674"/><text class="flow-label" x="870" y="620">Pack + identity</text>
    <path class="flow socket-flow" data-flow="socket" d="M1190 694H1205"/><text class="flow-label" x="1160" y="732">Local socket</text>
    <path class="flow delivery-flow" data-flow="delivery" d="M1350 454C1450 500 1460 580 1445 630"/><text class="flow-label" x="1435" y="545">cf push</text>
  </g>
</g>
```

- [ ] **Step 2: Add styles for layers, app containers, components, ports, and SSH arrows**

Add these style rules alongside the existing SVG rules:

```css
.layer { fill:#0d1a25; stroke:#35566d; stroke-width:2; }
.ingress-layer { fill:#102638; }
.diego-layer { fill:#0e2530; stroke:#3f9cff; }
.layer-title,.app-label { fill:#79bdff; font-size:12px; font-weight:850; letter-spacing:.13em; }
.layer-copy { fill:var(--muted); font-size:14px; }
.ingress-service { fill:white; font-size:18px; font-weight:800; }
.app-container>rect { fill:#111f2b; stroke:var(--poc); stroke-width:3; }
.component rect { fill:#172b3b; stroke:#4c687c; stroke-width:1.5; }
.component text { fill:#dce7ee; font-size:12px; font-weight:700; }
.port { stroke:#071019; stroke-width:3; }
.browser-port { fill:#54d6ff; }
.ssh-port { fill:#65db8b; }
.control-port { fill:#3f9cff; }
.pack-port { fill:#ffc857; }
.browser-flow { stroke:#54d6ff; }
.ssh-flow { stroke:#65db8b; marker-end:url(#arrow-ssh); }
.control-flow { stroke:#3f9cff; }
.pack-flow { stroke:#ffc857; }
.socket-flow { stroke:#d8b7ff; }
.delivery-flow { stroke:#f2f4f5; }
```

Add an `arrow-ssh` marker using `#65db8b` in the SVG `<defs>`.

- [ ] **Step 3: Run focused tests**

Run: `go test ./scripts -run ArchitectureDiagram -v`

Expected: PASS, 3 tests.

Run: `npm test -- --run src/architecture-diagram.test.ts` from `web/`

Expected: FAIL only if the old `Manager app` selector no longer exists.

- [ ] **Step 4: Preserve the manager tooltip selector if needed**

If the Vitest test fails, make `manager-app` itself interactive by adding:

```html
class="app-container node poc-node" tabindex="0" role="button"
aria-label="Manager app"
data-title="Manager app"
data-short="Serves the UI and reconciles sandbox lifecycle."
data-detail="Working POC: API, file-backed state, sanitized operation evidence, CF CLI adapter, and reverse proxy to lead Collie."
```

Then rerun: `npm test -- --run src/architecture-diagram.test.ts` from `web/`

Expected: PASS, 3 tests.

### Task 3: Verify The Revised Artifact

**Files:**
- Modify if needed: `docs/presentations/cf-herdr-architecture.html`
- Test: `scripts/architecture_diagram_test.go`
- Test: `web/src/architecture-diagram.test.ts`

- [ ] **Step 1: Run all automated verification**

Run: `go test ./...`

Expected: PASS.

Run: `npm test -- --run` from `web/`

Expected: PASS, 36 tests.

Run: `npm run typecheck` from `web/`

Expected: PASS.

Run: `git diff --check`

Expected: no output.

- [ ] **Step 2: Publish the revision to the active visual companion**

Run:

```bash
cp docs/presentations/cf-herdr-architecture.html .superpowers/brainstorm/16690-1789372682/content/layered-architecture.html
```

Expected: `http://10.246.103.88:50672` displays the revised page.

- [ ] **Step 3: Perform visual acceptance outside the VM**

At stage two verify:

1. Collie browser and Developer terminal are the top-most clients.
2. Gorouter and SSH Proxy form one full-width ingress band.
3. Matching arrow colors clearly reconnect paths above and below ingress.
4. Manager and Sandbox are visibly bounded app containers with nested components.
5. CF API sits beside the app containers and is not visually hosted on Diego.
6. Diego is a full-width layer below the application containers.
7. The terminal path says only `cf ssh` / SSH and does not explain handshake details.
8. Labels remain readable and flow lines do not collide with component text at 16:9.
