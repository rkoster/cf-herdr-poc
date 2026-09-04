import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { createMemoryRouter, RouterProvider } from "react-router";

import { routes } from "@/router";
import { existingSandbox } from "@/test/handlers";
import { server } from "@/test/setup";

function renderManager() {
  const router = createMemoryRouter(routes, { basename: "/manager", initialEntries: ["/manager/"] });
  return { router, ...render(<RouterProvider router={router} />) };
}

test("renders existing sandboxes and creation fields", async () => {
  renderManager();
  expect(await screen.findByRole("heading", { name: "demo-ruby" })).toBeInTheDocument();
  expect(screen.getAllByText("ruby_buildpack")).toHaveLength(2);
  expect(screen.getByText(/abc1234/)).toBeInTheDocument();
  expect(screen.getByLabelText("Name")).toBeInTheDocument();
  expect(screen.getByLabelText("Git repository")).toBeInTheDocument();
  expect(screen.getByLabelText("Buildpack")).toHaveDisplayValue("ruby_buildpack");
  expect(screen.getByRole("link", { name: "Agents" })).toHaveAttribute("href", "/collie/");
});

test("shows operation timing and friction summaries", async () => {
  server.use(http.get("/manager/api/sandboxes", () => HttpResponse.json([{
    ...existingSandbox,
    operations: [
      { name: "stage", summary: "Uploaded app bits", startedAt: "2026-09-03T10:01:00Z", duration: 4_200_000_000, success: true },
      { name: "route", startedAt: "2026-09-03T10:02:00Z", duration: 1_500_000_000, success: false, error: "route propagation timed out" },
    ],
  }])));
  renderManager();
  expect(await screen.findByText("Uploaded app bits")).toBeInTheDocument();
  expect(screen.getByText("4.2s")).toBeInTheDocument();
  expect(screen.getByText("route propagation timed out")).toBeInTheDocument();
});

test("validates locally and posts exact creation JSON", async () => {
  const user = userEvent.setup();
  let posted: unknown;
  server.use(http.post("/manager/api/sandboxes", async ({ request }) => {
    posted = await request.json();
    return HttpResponse.json(existingSandbox, { status: 202 });
  }));
  renderManager();
  await screen.findByLabelText("Buildpack");
  await user.click(screen.getByRole("button", { name: "Create sandbox" }));
  expect(screen.getByText("Use lowercase letters, numbers, and hyphens.")).toBeInTheDocument();
  expect(posted).toBeUndefined();
  await user.type(screen.getByLabelText("Name"), "new-app");
  await user.type(screen.getByLabelText("Git repository"), "https://git.example/new-app.git");
  await user.selectOptions(screen.getByLabelText("Buildpack"), "nodejs_buildpack");
  await user.click(screen.getByRole("button", { name: "Create sandbox" }));
  await waitFor(() => expect(posted).toEqual({
    name: "new-app",
    repository: "https://git.example/new-app.git",
    buildpack: "nodejs_buildpack",
  }));
});

test("confirms delete, retries failures, and opens the enrolled agent", async () => {
  const user = userEvent.setup();
  let deleted = 0;
  let retried = 0;
  server.use(
    http.get("/manager/api/sandboxes", () => HttpResponse.json([{ ...existingSandbox, phase: "failed", lastError: "route propagation timed out" }])),
    http.delete("/manager/api/sandboxes/demo-ruby", () => { deleted++; return new HttpResponse(null, { status: 202 }); }),
    http.post("/manager/api/sandboxes/demo-ruby/retry", () => { retried++; return new HttpResponse(null, { status: 202 }); }),
  );
  renderManager();
  expect(await screen.findByRole("alert")).toHaveTextContent("route propagation timed out");
  await user.click(screen.getByRole("button", { name: "Retry demo-ruby" }));
  await waitFor(() => expect(retried).toBe(1));
  await user.click(screen.getByRole("button", { name: "Delete demo-ruby" }));
  expect(deleted).toBe(0);
  await user.click(screen.getByRole("button", { name: "Confirm delete demo-ruby" }));
  await waitFor(() => expect(deleted).toBe(1));
  expect(screen.queryByRole("link", { name: "Open Agents for demo-ruby" })).not.toBeInTheDocument();
});

test("logs in without retaining or rendering the token and logs out", async () => {
  const user = userEvent.setup();
  let tokenBody: unknown;
  let authenticated = false;
  let loggedOut = false;
  server.use(
    http.get("/manager/api/config", () => authenticated ? HttpResponse.json({ buildpacks: ["ruby_buildpack"] }) : HttpResponse.json({ error: "unauthorized" }, { status: 401 })),
    http.get("/manager/api/sandboxes", () => authenticated ? HttpResponse.json([]) : HttpResponse.json({ error: "unauthorized" }, { status: 401 })),
    http.post("/manager/api/session", async ({ request }) => { tokenBody = await request.json(); authenticated = true; return new HttpResponse(null, { status: 204 }); }),
    http.delete("/manager/api/session", () => { authenticated = false; loggedOut = true; return new HttpResponse(null, { status: 204 }); }),
  );
  renderManager();
  const input = await screen.findByLabelText("Manager token");
  await user.type(input, "one-time-secret");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  await screen.findByRole("heading", { name: "Sandboxes" });
  expect(tokenBody).toEqual({ token: "one-time-secret" });
  expect(screen.queryByDisplayValue("one-time-secret")).not.toBeInTheDocument();
  expect(document.body).not.toHaveTextContent("one-time-secret");
  expect(localStorage).toHaveLength(0);
  expect(sessionStorage).toHaveLength(0);
  expect(window.location.href).not.toContain("one-time-secret");
  await user.click(screen.getByRole("button", { name: "Log out" }));
  await waitFor(() => expect(loggedOut).toBe(true));
  expect(await screen.findByLabelText("Manager token")).toBeInTheDocument();
});

test("returns to login after a mutation receives 401 and reports other API errors", async () => {
  const user = userEvent.setup();
  server.use(http.post("/manager/api/sandboxes", () => HttpResponse.json({ error: "unauthorized" }, { status: 401 })));
  renderManager();
  await screen.findByLabelText("Buildpack");
  await user.type(screen.getByLabelText("Name"), "new-app");
  await user.type(screen.getByLabelText("Git repository"), "https://git.example/new.git");
  await user.click(screen.getByRole("button", { name: "Create sandbox" }));
  expect(await screen.findByLabelText("Manager token")).toBeInTheDocument();

  server.use(http.post("/manager/api/session", () => HttpResponse.json({ error: "invalid token" }, { status: 401 })));
  await user.type(screen.getByLabelText("Manager token"), "wrong");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("invalid token");
});

test("polls active work at 1500ms, terminal work at 10s, and pauses while hidden", async () => {
  vi.useFakeTimers();
  let requests = 0;
  server.use(http.get("/manager/api/sandboxes", () => { requests++; return HttpResponse.json([{ ...existingSandbox, phase: "staging" }]); }));
  renderManager();
  await act(async () => { await vi.runOnlyPendingTimersAsync(); });
  const baseline = requests;
  await act(async () => { await vi.advanceTimersByTimeAsync(1499); });
  expect(requests).toBe(baseline);
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(requests).toBeGreaterThan(baseline);

  Object.defineProperty(document, "hidden", { configurable: true, value: true });
  fireEvent(document, new Event("visibilitychange"));
  const hiddenCount = requests;
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(requests).toBe(hiddenCount);
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
  fireEvent(document, new Event("visibilitychange"));
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(requests).toBeGreaterThan(hiddenCount);
});

test("polls terminal work at 10s and reports polling errors", async () => {
  vi.useFakeTimers();
  let requests = 0;
  server.use(http.get("/manager/api/sandboxes", () => {
    requests++;
    if (requests > 1) return HttpResponse.json({ error: "poll failed" }, { status: 500 });
    return HttpResponse.json([existingSandbox]);
  }));
  renderManager();
  await act(async () => { await vi.runOnlyPendingTimersAsync(); });
  const baseline = requests;
  await act(async () => { await vi.advanceTimersByTimeAsync(9999); });
  expect(requests).toBe(baseline);
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(requests).toBeGreaterThan(baseline);
  expect(screen.getByRole("alert")).toHaveTextContent("poll failed");
});
