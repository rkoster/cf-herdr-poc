import { useEffect, useState, type FormEvent } from "react";
import { Outlet, useLoaderData, useRevalidator, useRouteError } from "react-router";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, APIError } from "@/lib/api";
import type { ManagerConfig, SandboxView } from "@/lib/types";

export interface RootData { authenticated: boolean; config?: ManagerConfig; sandboxes?: SandboxView[]; expire?: () => void }

export async function rootLoader(): Promise<RootData> {
  try {
    const [config, sandboxes] = await Promise.all([api.config(), api.sandboxes()]);
    return { authenticated: true, config, sandboxes };
  } catch (error) {
    if (error instanceof APIError && error.status === 401) return { authenticated: false };
    throw error;
  }
}

function Login() {
  const revalidator = useRevalidator();
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    const submitted = token;
    setToken("");
    try {
      await api.login(submitted);
      revalidator.revalidate();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Sign in failed");
    }
  }
  return <main className="login-shell"><form className="login" onSubmit={submit}><span className="eyebrow">Restricted operator surface</span><h1>Sandbox Manager</h1><p>Exchange the deployment token for a secure browser session.</p><label>Manager token<Input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" /></label>{error && <p role="alert" className="form-error">{error}</p>}<Button>Sign in</Button></form></main>;
}

export function RootRoute() {
  const data = useLoaderData() as RootData;
  const [authenticated, setAuthenticated] = useState(data.authenticated);
  useEffect(() => setAuthenticated(data.authenticated), [data.authenticated]);
  if (!authenticated) return <Login />;
  return <Outlet context={{ ...data, expire: () => setAuthenticated(false) }} />;
}

export function RootError() {
  const error = useRouteError();
  const message = error instanceof Error ? error.message : "The manager API did not return a usable response.";
  return <main className="login-shell"><section className="login"><span className="eyebrow">Gateway unavailable</span><h1>Manager could not load</h1><p role="alert">{message} Reload to try again.</p></section></main>;
}
