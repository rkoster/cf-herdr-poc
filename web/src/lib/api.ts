import type { CreateSandboxInput, ManagerConfig, SandboxView } from "@/lib/types";

export class APIError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/manager/api${path}`, {
    credentials: "same-origin",
    ...init,
    headers: init?.body ? { "Content-Type": "application/json", ...init.headers } : init?.headers,
  });
  if (!response.ok) {
    let message = response.statusText || "Request failed";
    try {
      const body = (await response.json()) as { error?: string };
      message = body.error ?? message;
    } catch {
      // Plain-text backend validation errors are intentionally reduced to a stable message.
    }
    throw new APIError(message, response.status);
  }
  return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}

export const api = {
  config: () => request<ManagerConfig>("/config"),
  sandboxes: () => request<SandboxView[]>("/sandboxes"),
  login: (token: string) => request<void>("/session", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => request<void>("/session", { method: "DELETE" }),
  create: (input: CreateSandboxInput) => request<SandboxView>("/sandboxes", { method: "POST", body: JSON.stringify(input) }),
  retry: (name: string) => request<void>(`/sandboxes/${encodeURIComponent(name)}/retry`, { method: "POST" }),
  remove: (name: string) => request<void>(`/sandboxes/${encodeURIComponent(name)}`, { method: "DELETE" }),
};
