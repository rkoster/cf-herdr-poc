export type SandboxPhase =
  | "creating"
  | "preparing-invite"
  | "staging"
  | "discovering-app"
  | "starting"
  | "securing-route"
  | "securing-manager-route"
  | "configuring-enrollment"
  | "waiting-for-app"
  | "waiting-for-route"
  | "triggering-enrollment"
  | "joining-pack"
  | "ready"
  | "deleting"
  | "failed";

export interface OperationView {
  name: string;
  summary?: string;
  startedAt: string;
  duration: number;
  success: boolean;
  error?: string;
}

export interface SandboxView {
  name: string;
  repository: string;
  revision?: string;
  buildpack: string;
  desired: "present" | "deleted";
  phase: SandboxPhase;
  resumePhase?: SandboxPhase;
  packMemberId?: string;
  lastError?: string;
  operations?: OperationView[];
  createdAt: string;
  updatedAt: string;
}

export interface ManagerConfig {
  buildpacks: string[];
}

export interface CreateSandboxInput {
  name: string;
  repository: string;
  buildpack: string;
}
