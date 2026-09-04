import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { SandboxPhase, SandboxView } from "@/lib/types";

const creationPhases = [
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
] as const satisfies ReadonlyArray<readonly [Exclude<SandboxPhase, "deleting" | "failed">, string]>;

const phaseLabels: Record<SandboxPhase, string> = {
  ...Object.fromEntries(creationPhases),
  deleting: "Teardown in progress",
  failed: "Failed",
} as Record<SandboxPhase, string>;

function age(from: string) {
  const seconds = Math.max(0, Math.floor((Date.now() - Date.parse(from)) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}

function duration(nanoseconds: number) {
  const milliseconds = nanoseconds / 1_000_000;
  return milliseconds < 1000 ? `${milliseconds.toFixed(0)}ms` : `${(milliseconds / 1000).toFixed(1)}s`;
}

interface Props {
  sandbox: SandboxView;
  pending?: "retry" | "delete";
  onRetry: () => Promise<boolean>;
  onDelete: () => Promise<boolean>;
}

export function SandboxCard({ sandbox, pending, onRetry, onDelete }: Props) {
  const [confirming, setConfirming] = useState(false);
  const shownPhase = sandbox.phase === "failed" ? sandbox.resumePhase : sandbox.phase;
  const current = creationPhases.findIndex(([phase]) => phase === shownPhase);
  return (
    <article className={`sandbox-card status-${sandbox.phase}`} aria-busy={pending !== undefined}>
      <header className="card-header">
        <div><span className="eyebrow">Sandbox</span><h3>{sandbox.name}</h3></div>
        <span className="status"><i />{sandbox.phase === "failed" && sandbox.resumePhase ? `Failed; retry resumes at ${phaseLabels[sandbox.resumePhase]}` : phaseLabels[sandbox.phase]}</span>
      </header>
      <dl className="facts">
        <div><dt>Buildpack</dt><dd>{sandbox.buildpack}</dd></div>
        <div><dt>Source</dt><dd>{sandbox.repository}{sandbox.revision && <small> @ {sandbox.revision}</small>}</dd></div>
        <div><dt>Elapsed</dt><dd>{age(sandbox.updatedAt)} phase / created {age(sandbox.createdAt)} ago</dd></div>
      </dl>
      {sandbox.phase === "deleting" ? <p className="teardown">Teardown in progress</p> : <ol className="phase-rail" aria-label={`Lifecycle for ${sandbox.name}`}>
        {creationPhases.map(([phase, label], index) => <li key={phase} className={`${index < current ? "complete" : index === current ? "current" : ""}${sandbox.phase === "failed" && index === current ? " failed" : ""}`}><i /><span>{label}</span></li>)}
      </ol>}
      {sandbox.operations && sandbox.operations.length > 0 && <section className="operations" aria-label={`Operation timings for ${sandbox.name}`}>
        <span className="eyebrow">Timing / friction summary</span>
        <ul>{sandbox.operations.map((operation) => <li key={`${operation.name}-${operation.startedAt}`} className={operation.success ? "" : "failed"}><span>{operation.summary ?? operation.name}</span><time>{duration(operation.duration)}</time>{operation.error && <small>{operation.error}</small>}</li>)}</ul>
      </section>}
      {sandbox.lastError && <pre className="friction" role="alert"><span>Friction output</span>{sandbox.lastError}</pre>}
      {pending && <span className="sr-only" role="status">{pending === "retry" ? `Retrying ${sandbox.name}` : `Deleting ${sandbox.name}`}</span>}
      <footer className="card-actions">
        {sandbox.phase === "failed" && <Button disabled={pending !== undefined} onClick={() => void onRetry()} aria-label={`Retry ${sandbox.name}`}>{pending === "retry" ? "Retrying..." : "Retry failed"}</Button>}
        {sandbox.phase === "ready" && sandbox.packMemberId && <a className="button" href={`/collie/?h=${encodeURIComponent(sandbox.packMemberId)}`} aria-label={`Open Agents for ${sandbox.name}`}>Open Agents</a>}
        {confirming ? <Button disabled={pending !== undefined} className="danger" onClick={() => void onDelete()} aria-label={`Confirm delete ${sandbox.name}`}>{pending === "delete" ? "Deleting..." : "Confirm delete"}</Button> : <Button disabled={pending !== undefined} className="quiet" onClick={() => setConfirming(true)} aria-label={`Delete ${sandbox.name}`}>Delete</Button>}
        {confirming && <Button disabled={pending !== undefined} className="quiet" onClick={() => setConfirming(false)}>Cancel</Button>}
      </footer>
    </article>
  );
}
