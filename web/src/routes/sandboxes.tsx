import { useEffect, useState } from "react";
import { useOutletContext, useRevalidator } from "react-router";

import { CreateSandboxForm } from "@/components/create-sandbox-form";
import { SandboxCard } from "@/components/sandbox-card";
import { Button } from "@/components/ui/button";
import { api, APIError } from "@/lib/api";
import type { RootData } from "@/routes/root";

export function SandboxesRoute() {
  const data = useOutletContext<RootData>();
  const revalidator = useRevalidator();
  const [error, setError] = useState("");
  const sandboxes = data.sandboxes ?? [];
  const active = sandboxes.some(({ phase }) => phase !== "ready" && phase !== "failed");

  useEffect(() => {
    let timer: number | undefined;
    const schedule = () => {
      window.clearTimeout(timer);
      if (!document.hidden) timer = window.setTimeout(() => { revalidator.revalidate(); schedule(); }, active ? 1500 : 10000);
    };
    const visibility = () => schedule();
    schedule();
    document.addEventListener("visibilitychange", visibility);
    return () => { window.clearTimeout(timer); document.removeEventListener("visibilitychange", visibility); };
  }, [active, revalidator]);

  async function mutate(run: () => Promise<unknown>) {
    setError("");
    try {
      await run();
      revalidator.revalidate();
    } catch (reason) {
      if (reason instanceof APIError && reason.status === 401) data.expire?.();
      else setError(reason instanceof Error ? reason.message : "Request failed");
    }
  }

  return (
    <div className="app-shell">
      <header className="topbar"><a className="wordmark" href="/manager/">CF HERDR <span>/ manager</span></a><nav aria-label="Product"><a aria-current="page" href="/manager/">Sandboxes</a><a href="/collie/">Agents</a></nav><Button className="quiet" onClick={() => void api.logout().then(() => data.expire?.()).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "Logout failed"))}>Log out</Button></header>
      <main>
        <section className="hero"><div><span className="eyebrow">Cloud Foundry operations</span><h1>Sandboxes</h1></div><p><strong>{sandboxes.length.toString().padStart(2, "0")}</strong> isolated workloads<br />tracked through enrollment</p></section>
        <CreateSandboxForm buildpacks={data.config?.buildpacks ?? []} onCreate={(input) => mutate(() => api.create(input))} />
        {error && <p className="page-error" role="alert">{error}</p>}
        <section className="fleet" aria-label="Sandbox fleet"><div className="section-heading"><div><span className="eyebrow">Observed state</span><h2>Lifecycle rail</h2></div><span className="live-marker"><i /> polling {active ? "1.5s" : "10s"}</span></div>{sandboxes.length === 0 ? <p className="empty">No sandboxes provisioned.</p> : <div className="card-grid">{sandboxes.map((sandbox) => <SandboxCard key={sandbox.name} sandbox={sandbox} onRetry={() => mutate(() => api.retry(sandbox.name))} onDelete={() => mutate(() => api.remove(sandbox.name))} />)}</div>}</section>
      </main>
    </div>
  );
}
