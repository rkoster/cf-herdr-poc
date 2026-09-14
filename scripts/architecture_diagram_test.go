package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readArchitectureDiagram(t *testing.T) string {
	t.Helper()
	root := packageRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "docs", "presentations", "cf-herdr-architecture.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestArchitectureDiagramIsStandalone(t *testing.T) {
	html := readArchitectureDiagram(t)
	for _, forbidden := range []string{
		`<link `,
		`<script src=`,
		`url("http`,
		`url('http`,
		`href="http`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("diagram contains external dependency %q", forbidden)
		}
	}
	for _, required := range []string{
		`<!DOCTYPE html>`,
		`<svg`,
		`viewBox="0 0 1600 900"`,
		`<style>`,
		`<script>`,
		`@media print`,
		`prefers-reduced-motion`,
		`<noscript>`,
		`aspect-ratio: 16 / 9`,
		`@media (max-width: 900px)`,
		`@media (prefers-reduced-motion: reduce)`,
		`body[data-active-stage="1"] [data-stage="2"]`,
		`body[data-active-stage="2"] [data-stage="3"]`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("diagram missing %q", required)
		}
	}
}

func TestArchitectureDiagramCoversApprovedStory(t *testing.T) {
	html := readArchitectureDiagram(t)
	for _, required := range []string{
		`data-stage="1"`, `data-stage="2"`, `data-stage="3"`,
		`Choose the work`, `Start an agent`, `Steer from anywhere`, `Ship through CF`,
		`id="clients-layer"`, `CLIENTS`, `Developer Browser`, `Developer Terminal`, `cf ssh`,
		`x="120" y="145" width="300"`, `x="1205" y="145" width="300"`,
		`client-node`,
		`id="ingress-layer"`, `INGRESS`, `Gorouter`, `SSH Proxy`,
		`id="apps-layer"`, `APPLICATIONS + CONTROL PLANE`,
		`id="manager-app"`, `APP · CF HERDR MANAGER`, `Manager API`, `+ reconciler`,
		`Lead Collie`,
		`id="sandbox-app"`, `APP · AGENT SANDBOX`, `id="herdr-runtime"`,
		`Sandbox Collie`, `HERDR`, `OpenCode`, `agent`, `Shell`, `session`, `+ CF CLI`,
		`id="cf-api"`, `CF API`, `Outside Diego`,
		`id="diego-region"`, `DIEGO`,
		`x="330" y="770" width="1175"`,
		`x="917.5" y="805" text-anchor="middle"`,
		`body[data-active-stage="2"] [data-stage="1"]`,
		`data-layout="expanded-apps platform-table"`, `class="row-label"`,
		`data-grid="normalized"`, `class="component-title"`,
		`class="component-copy"`, `class="app-title"`,
		`data-label-position="bottom"`,
		`data-content-position="upper"`,
		`class="app-container gray-style"`, `class="component sandbox-style"`,
		`.sandbox-style rect`,
		`id="manager-api-core"`,
		`class="process purple-style"`,
		`.component.yellow-style rect`, `.process.purple-style rect`,
		`class="node sandbox-style"`,
		`class="herdr-runtime node sandbox-style"`,
		`id="pack-route"`, `class="wide-route"`, `marker-end="url(#wide-arrow)"`,
		`id="browser-route"`, `class="wide-route"`,
		`d="M420 181H642.5V445"`,
		`id="manager-collie-route"`, `class="wide-route"`,
		`d="M598.75 445V417.5H491.25V445"`,
		`id="sandbox-collie-herdr-route"`, `class="wide-route"`,
		`d="M947.5 445V417.5H1068.125V445"`,
		`id="terminal-herdr-route"`, `class="wide-route"`,
		`d="M1126.25 445V181H1205"`,
		`id="manager-cf-api-route"`, `class="wide-route"`,
		`d="M447.5 445V302.5H210V390"`,
		`id="connections-layer"`,
		`stroke="#7b9db5"`,
		`d="M905 445V302.5H686.25V445"`,
		`markerWidth="28"`, `markerHeight="28"`, `markerUnits="userSpaceOnUse"`,
		`<circle cx="14" cy="14" r="14"`,
		`class="route-endpoint" cx="905" cy="445" r="14"`,
		`x="70" y="365" width="1460" height="485"`,
		`id="gorouter-cell"`, `id="ssh-proxy-cell"`,
		`x="120" y="265" width="870"`, `x="1010" y="265" width="495"`,
		`id="gorouter-cell" class="cf-system-component"`,
		`id="ssh-proxy-cell" class="cf-system-component"`,
		`class="cf-system-component cf-api-region"`,
		`class="cf-system-component diego-region"`,
		`Workspace primitive`, `Identity &amp; authorization`, `Isolation &amp; tenancy`,
		`State &amp; lifecycle`, `Connectivity`, `Observability &amp; audit`, `Resource governance`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("diagram missing approved content %q", required)
		}
	}
	for _, forbidden := range []string{
		`CF SSH handshake`,
		`Cloud Controller · Diego`,
		`DEVELOPER EDGE`,
		`id="diego-layer"`,
		`sandbox-bootstrap`,
		`.herdr-runtime>rect`,
		`Schedules and runs application containers`,
		`Lifecycle state`,
		`CF CLI adapter`,
		`Lead Collie · loopback`,
		`class="flow`,
		`class="port`,
		`id="arrow-browser"`, `id="arrow-control"`, `id="arrow-poc"`, `id="arrow-socket"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("diagram contains obsolete topology %q", forbidden)
		}
	}
}

func TestArchitectureDiagramExposesAccessibleControls(t *testing.T) {
	html := readArchitectureDiagram(t)
	for _, required := range []string{
		`aria-label="Architecture presentation stages"`,
		`data-stage-button="1"`, `data-stage-button="2"`, `data-stage-button="3"`,
		`id="reset-presentation"`, `id="diagram-tooltip"`, `role="tooltip"`,
		`id="speaker-notes"`, `aria-live="polite"`, `tabindex="0"`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("diagram missing accessibility contract %q", required)
		}
	}
}

func TestArchitectureDiagramLayersConnectionsAboveAppsAndBelowIngress(t *testing.T) {
	html := readArchitectureDiagram(t)
	apps := strings.Index(html, `id="apps-layer"`)
	connections := strings.Index(html, `id="connections-layer"`)
	ingress := strings.Index(html, `id="ingress-layer"`)
	if apps < 0 || connections < 0 || ingress < 0 {
		t.Fatal("diagram is missing apps, connections, or ingress layer")
	}
	if !(apps < connections && connections < ingress) {
		t.Fatalf("SVG paint order = apps:%d connections:%d ingress:%d, want apps < connections < ingress", apps, connections, ingress)
	}
}
