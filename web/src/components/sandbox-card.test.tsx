import { render, screen, within } from "@testing-library/react";

import { SandboxCard } from "@/components/sandbox-card";
import type { SandboxPhase, SandboxView } from "@/lib/types";
import { existingSandbox } from "@/test/handlers";

const creationPhases: Array<[SandboxPhase, string]> = [
  ["creating", "Creating record"],
  ["preparing-invite", "Preparing invite"],
  ["staging", "Staging application"],
  ["discovering-app", "Discovering application"],
  ["starting", "Starting application"],
  ["securing-route", "Securing sandbox route"],
  ["securing-manager-route", "Securing manager route"],
  ["configuring-enrollment", "Configuring enrollment"],
  ["waiting-for-app", "Waiting for application"],
  ["waiting-for-route", "Waiting for route"],
  ["triggering-enrollment", "Triggering enrollment"],
  ["joining-pack", "Joining Pack"],
  ["ready", "Ready"],
];

function card(phase: SandboxPhase, resumePhase?: SandboxPhase) {
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase, resumePhase };
  return render(<SandboxCard sandbox={sandbox} onRetry={async () => true} onDelete={async () => true} />);
}

test.each(creationPhases)("renders actual lifecycle phase %s as %s", (phase, label) => {
  card(phase);
  const lifecycle = screen.getByRole("list", { name: "Lifecycle for demo-ruby" });
  expect(within(lifecycle).getByText(label).closest("li")).toHaveClass("current");
  expect(within(lifecycle).getAllByRole("listitem")).toHaveLength(creationPhases.length);
});

test("renders deleting as a separate teardown state", () => {
  card("deleting");
  expect(screen.getByText("Teardown in progress", { selector: ".teardown" })).toBeInTheDocument();
  expect(screen.queryByRole("list", { name: "Lifecycle for demo-ruby" })).not.toBeInTheDocument();
});

test("renders failed at its resume phase without presenting failure as progress", () => {
  card("failed", "waiting-for-route");
  expect(screen.getByText("Failed; retry resumes at Waiting for route")).toBeInTheDocument();
  expect(screen.getByText("Waiting for route").closest("li")).toHaveClass("current", "failed");
});

test("renders controlled retry busy state", () => {
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase: "failed", resumePhase: "waiting-for-route" };
  render(<SandboxCard sandbox={sandbox} pending="retry" onRetry={async () => true} onDelete={async () => true} />);
  const retry = screen.getByRole("button", { name: "Retry demo-ruby" });
  expect(retry).toBeDisabled();
  expect(screen.getByRole("button", { name: "Delete demo-ruby" })).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("Retrying demo-ruby");
});
