import { useState, type FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { CreateSandboxInput } from "@/lib/types";

interface Props {
  buildpacks: string[];
  onCreate: (input: CreateSandboxInput) => Promise<boolean>;
}

export function CreateSandboxForm({ buildpacks, onCreate }: Props) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    const input = {
      name: String(form.get("name") ?? "").trim(),
      repository: String(form.get("repository") ?? "").trim(),
      buildpack: String(form.get("buildpack") ?? ""),
    };
    if (!/^[a-z][a-z0-9-]{0,47}$/.test(input.name)) {
      setError("Use lowercase letters, numbers, and hyphens.");
      return;
    }
    if (!/^(https?:\/\/|ssh:\/\/|git@[^:]+:).+/.test(input.repository)) {
      setError("Enter an HTTP, HTTPS, SSH, or git repository.");
      return;
    }
    setError("");
    setBusy(true);
    try {
      if (await onCreate(input)) element.reset();
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="create-form" onSubmit={submit} noValidate>
      <div className="section-heading">
        <div><span className="eyebrow">New workload</span><h2>Create sandbox</h2></div>
        <span className="sequence">01 / provision</span>
      </div>
      <div className="form-grid">
        <label>Name<Input name="name" autoComplete="off" placeholder="payments-api" /></label>
        <label className="repository-field">Git repository<Input name="repository" type="url" placeholder="https://git.example/team/app.git" /></label>
        <label>Buildpack<Select name="buildpack">{buildpacks.map((item) => <option key={item}>{item}</option>)}</Select></label>
        <Button disabled={busy}>{busy ? "Creating..." : "Create sandbox"}</Button>
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
    </form>
  );
}
