import { http, HttpResponse } from "msw";

export const existingSandbox = {
  name: "demo-ruby",
  repository: "https://git.example/team/demo.git",
  revision: "abc1234",
  buildpack: "ruby_buildpack",
  desired: "present",
  phase: "ready",
  packMemberId: "demo-ruby",
  createdAt: "2026-09-03T10:00:00Z",
  updatedAt: "2026-09-03T10:03:00Z",
  operations: [],
};

export const handlers = [
  http.get("/manager/api/config", () => HttpResponse.json({ buildpacks: ["ruby_buildpack", "nodejs_buildpack"] })),
  http.get("/manager/api/sandboxes", () => HttpResponse.json([existingSandbox])),
];
