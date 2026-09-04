import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { SandboxPhase, SandboxView } from "@/lib/types";

const steps = ["Requested", "Built", "Started", "Secured", "Enrolled", "Ready"];
const phaseStep: Record<SandboxPhase, number> = {
  creating: 0,
  "preparing-invite": 0,
  staging: 1,
  "discovering-app": 1,
  starting: 2,
  "securing-route": 3,
  "securing-manager-route": 3,
  "configuring-enrollment": 4,
  "waiting-for-app": 4,
  "waiting-for-route": 4,
  "triggering-enrollment": 4,
  "joining-pack": 4,
  ready: 5,
  deleting: 0,
  failed: 0,
};

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
  onRetry: () => Promise<void>;
  onDelete: () => Promise<void>;
}

export function SandboxCard({ sandbox, onRetry, onDelete }: Props) {
  const [confirming, setConfirming] = useState(false);
  const current = sandbox.phase === "failed" && sandbox.resumePhase ? phaseStep[sandbox.resumePhase] : phaseStep[sandbox.phase];
  return (
    <article className={`sandbox-card status-${sandbox.phase}`}>
      <header className="card-header">
        <div><span className="eyebrow">Sandbox</span><h3>{sandbox.name}</h3></div>
        <span className="status"><i />{sandbox.phase.replaceAll("-", " ")}</span>
      </header>
      <dl className="facts">
        <div><dt>Buildpack</dt><dd>{sandbox.buildpack}</dd></div>
        <div><dt>Source</dt><dd>{sandbox.repository}{sandbox.revision && <small> @ {sandbox.revision}</small>}</dd></div>
        <div><dt>Elapsed</dt><dd>{age(sandbox.updatedAt)} phase / created {age(sandbox.createdAt)} ago</dd></div>
      </dl>
      <ol className="phase-rail" aria-label={`Lifecycle for ${sandbox.name}`}>
        {steps.map((step, index) => <li key={step} className={index < current ? "complete" : index === current ? "current" : ""}><i /><span>{step}</span></li>)}
      </ol>
      {sandbox.operations && sandbox.operations.length > 0 && <section className="operations" aria-label={`Operation timings for ${sandbox.name}`}>
        <span className="eyebrow">Timing / friction summary</span>
        <ul>{sandbox.operations.map((operation) => <li key={`${operation.name}-${operation.startedAt}`} className={operation.success ? "" : "failed"}><span>{operation.summary ?? operation.name}</span><time>{duration(operation.duration)}</time>{operation.error && <small>{operation.error}</small>}</li>)}</ul>
      </section>}
      {sandbox.lastError && <pre className="friction" role="alert"><span>Friction output</span>{sandbox.lastError}</pre>}
      <footer className="card-actions">
        {sandbox.phase === "failed" && <Button onClick={() => void onRetry()} aria-label={`Retry ${sandbox.name}`}>Retry failed</Button>}
        {sandbox.phase === "ready" && sandbox.packMemberId && <a className="button" href={`/collie/?h=${encodeURIComponent(sandbox.packMemberId)}`} aria-label={`Open Agents for ${sandbox.name}`}>Open Agents</a>}
        {confirming ? <Button className="danger" onClick={() => void onDelete()} aria-label={`Confirm delete ${sandbox.name}`}>Confirm delete</Button> : <Button className="quiet" onClick={() => setConfirming(true)} aria-label={`Delete ${sandbox.name}`}>Delete</Button>}
        {confirming && <Button className="quiet" onClick={() => setConfirming(false)}>Cancel</Button>}
      </footer>
    </article>
  );
}
