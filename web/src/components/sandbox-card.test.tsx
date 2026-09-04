import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";

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
  return render(<SandboxCard sandbox={sandbox} onRetry={async () => {}} onDelete={async () => {}} />);
}

function deferred() {
  let resolve!: () => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<void>((accept, decline) => { resolve = accept; reject = decline; });
  return { promise, reject, resolve };
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

test("serializes retry and restores controls after failure", async () => {
  const user = userEvent.setup();
  const request = deferred();
  const onRetry = vi.fn(() => request.promise);
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase: "failed", resumePhase: "waiting-for-route" };
  render(<SandboxCard sandbox={sandbox} onRetry={onRetry} onDelete={async () => {}} />);

  const retry = screen.getByRole("button", { name: "Retry demo-ruby" });
  await user.dblClick(retry);
  expect(onRetry).toHaveBeenCalledTimes(1);
  expect(retry).toBeDisabled();
  expect(screen.getByRole("button", { name: "Delete demo-ruby" })).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("Retrying demo-ruby");

  await act(async () => request.reject(new Error("retry failed")));
  expect(retry).toBeEnabled();
  expect(screen.getByRole("button", { name: "Delete demo-ruby" })).toBeEnabled();
});

test("serializes confirmed deletion and locks confirmation controls", async () => {
  const user = userEvent.setup();
  const request = deferred();
  const onDelete = vi.fn(() => request.promise);
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase: "failed", resumePhase: "waiting-for-route" };
  render(<SandboxCard sandbox={sandbox} onRetry={async () => {}} onDelete={onDelete} />);

  await user.click(screen.getByRole("button", { name: "Delete demo-ruby" }));
  const confirm = screen.getByRole("button", { name: "Confirm delete demo-ruby" });
  await user.dblClick(confirm);
  expect(onDelete).toHaveBeenCalledTimes(1);
  expect(confirm).toBeDisabled();
  expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Retry demo-ruby" })).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("Deleting demo-ruby");

  await act(async () => request.reject(new Error("delete failed")));
  expect(confirm).toBeEnabled();
  expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
});

test("does not update card state when a pending action settles after unmount", async () => {
  const request = deferred();
  const error = vi.spyOn(console, "error").mockImplementation(() => {});
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase: "failed", resumePhase: "waiting-for-route" };
  const view = render(<SandboxCard sandbox={sandbox} onRetry={() => request.promise} onDelete={async () => {}} />);
  await userEvent.click(screen.getByRole("button", { name: "Retry demo-ruby" }));
  view.unmount();
  await act(async () => request.resolve());
  expect(error).not.toHaveBeenCalled();
  error.mockRestore();
});

test("restores controls after failure under React Strict Mode", async () => {
  const request = deferred();
  const sandbox: SandboxView = { ...existingSandbox, desired: "present", phase: "failed", resumePhase: "waiting-for-route" };
  render(<StrictMode><SandboxCard sandbox={sandbox} onRetry={() => request.promise} onDelete={async () => {}} /></StrictMode>);
  const retry = screen.getByRole("button", { name: "Retry demo-ruby" });
  await userEvent.click(retry);
  await act(async () => request.reject(new Error("retry failed")));
  expect(retry).toBeEnabled();
});
