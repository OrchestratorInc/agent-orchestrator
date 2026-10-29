import { create } from "zustand";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { apiClient, apiErrorMessage } from "./api-client";

export type SpawnPreflightReport = components["schemas"]["SpawnPreflightResponse"];
export type SpawnPreflightCheck = components["schemas"]["SpawnPreflightCheck"];
export type SpawnPreflightBlocker = SpawnPreflightCheck["code"];
export type SpawnPreflightMethod = components["schemas"]["SpawnPreflightResolution"]["method"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type InstallJob = components["schemas"]["InstallJob"];

export type SpawnPreflightInput = {
	projectId: string;
	kind: "orchestrator" | "worker";
	/** The harness the spawn will request; empty uses the project's role agent. */
	harness?: string;
	mode?: "chat" | "tui";
	/** Pre-flight targets the local daemon only; a remote host spawns unchecked. */
	hostId?: string;
};

/** The harness the caller should spawn with once pre-flight clears. */
export type SpawnPreflightOutcome = { harness?: string };

/** The user dismissed a blocked pre-flight instead of fixing it. No spawn
 *  was attempted, so callers should not report a spawn failure. */
export class SpawnPreflightCancelledError extends Error {
	constructor() {
		super(appI18n.t("spawnPreflight.cancelled"));
		this.name = "SpawnPreflightCancelledError";
	}
}

export function isSpawnPreflightCancelled(error: unknown): error is SpawnPreflightCancelledError {
	return error instanceof SpawnPreflightCancelledError;
}

export type PendingSpawnPreflight = {
	input: SpawnPreflightInput;
	report: SpawnPreflightReport;
	resolve: (outcome: SpawnPreflightOutcome) => void;
	reject: (error: Error) => void;
};

type SpawnPreflightState = {
	pending: PendingSpawnPreflight | null;
	open: (pending: PendingSpawnPreflight) => void;
	update: (report: SpawnPreflightReport, input?: SpawnPreflightInput) => void;
	/** Settle the pending check: proceed with an outcome, or cancel. */
	settle: (outcome: SpawnPreflightOutcome | null) => void;
};

export const useSpawnPreflightStore = create<SpawnPreflightState>((set, get) => ({
	pending: null,
	open: (pending) => {
		// One dialog at a time: a newer blocked start replaces an older one,
		// which is cancelled rather than left hanging.
		get().pending?.reject(new SpawnPreflightCancelledError());
		set({ pending });
	},
	update: (report, input) => {
		const pending = get().pending;
		if (pending) set({ pending: { ...pending, report, input: input ?? pending.input } });
	},
	settle: (outcome) => {
		const pending = get().pending;
		if (!pending) return;
		set({ pending: null });
		if (outcome) pending.resolve(outcome);
		else pending.reject(new SpawnPreflightCancelledError());
	},
}));

export async function checkSpawnPreflight(
	input: SpawnPreflightInput,
	resolution?: { blockers: SpawnPreflightBlocker[]; method: SpawnPreflightMethod },
): Promise<SpawnPreflightReport> {
	const { data, error } = await apiClient.POST("/api/v1/sessions/preflight", {
		body: {
			projectId: input.projectId,
			kind: input.kind,
			...(input.harness ? { harness: input.harness } : {}),
			...(input.mode ? { mode: input.mode } : {}),
			...(resolution ? { resolution } : {}),
		},
	});
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not check spawn prerequisites"));
	return data;
}

/**
 * Checks the agent, runtime, and default branch before a local spawn. A
 * passing check resolves at once. A blocked one opens the shared fix dialog
 * and resolves when a re-check passes (or the user starts anyway past a soft
 * check), or rejects with SpawnPreflightCancelledError when dismissed. A
 * pre-flight that cannot run is not evidence of a problem: the spawn proceeds
 * and reports its own error.
 */
export async function ensureSpawnPreflight(input: SpawnPreflightInput): Promise<SpawnPreflightOutcome> {
	if (input.hostId) return { harness: input.harness };
	let report: SpawnPreflightReport;
	try {
		report = await checkSpawnPreflight(input);
	} catch (error) {
		console.warn("Spawn pre-flight unavailable; spawning unchecked", error);
		return { harness: input.harness };
	}
	if (!Array.isArray(report.checks)) return { harness: input.harness };
	if (report.ready) return { harness: input.harness ?? report.harness };
	return new Promise<SpawnPreflightOutcome>((resolve, reject) => {
		useSpawnPreflightStore.getState().open({ input, report, resolve, reject });
	});
}

async function updateProjectConfig(projectId: string, change: (config: ProjectConfig) => ProjectConfig): Promise<void> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
	if (error || data?.status !== "ok") throw new Error(apiErrorMessage(error, "Could not load the project"));
	const config = ("config" in data.project ? data.project.config : undefined) ?? {};
	const { error: saveError } = await apiClient.PUT("/api/v1/projects/{id}/config", {
		params: { path: { id: projectId } },
		body: { config: change(config) },
	});
	if (saveError) throw new Error(apiErrorMessage(saveError, "Could not save the project"));
}

/** Persists the orchestrator's agent so every later start uses it. Model and
 *  mode were tuned for the previous agent, so only harness-neutral
 *  permissions carry over. */
export function setProjectOrchestratorAgent(projectId: string, agent: string): Promise<void> {
	return updateProjectConfig(projectId, (config) => ({
		...config,
		orchestrator: {
			...config.orchestrator,
			agent,
			agentConfig: config.orchestrator?.agentConfig?.permissions
				? { permissions: config.orchestrator.agentConfig.permissions }
				: undefined,
		},
	}));
}

export function setProjectDefaultBranch(projectId: string, branch: string): Promise<void> {
	return updateProjectConfig(projectId, (config) => ({ ...config, defaultBranch: branch }));
}

const ACTIVE_INSTALL_STATUSES = new Set<InstallJob["status"]>(["running", "waiting_for_idle", "installing", "verifying"]);

/**
 * Runs AO's own install for an installable pre-flight blocker and waits for
 * the job to end: the agent through its install route with the recommended
 * method, or tmux through the system install route. Resolves with the final
 * job; the caller re-checks on success. The daemon owns every command.
 */
export async function runPreflightInstall(
	check: Pick<SpawnPreflightCheck, "code">,
	harness: string | undefined,
	{ intervalMs = 1_000, timeoutMs = 15 * 60_000 }: { intervalMs?: number; timeoutMs?: number } = {},
): Promise<InstallJob> {
	const runtime = check.code === "runtime_prerequisite_missing";
	const agent = harness ?? "";
	if (!runtime && (check.code !== "agent_not_installed" || !agent)) throw new Error("Nothing to install");
	const start = runtime
		? await apiClient.POST("/api/v1/system/install/{target}", { params: { path: { target: "tmux" } } })
		: await apiClient.POST("/api/v1/agents/{agent}/install", {
				params: { path: { agent } },
				body: { operation: "install" },
			});
	if (start.error || !start.data) throw new Error(apiErrorMessage(start.error, appI18n.t("spawnPreflight.installFailed")));
	let job: InstallJob = start.data;
	const deadline = Date.now() + timeoutMs;
	while (ACTIVE_INSTALL_STATUSES.has(job.status) && Date.now() < deadline) {
		await new Promise((resolve) => setTimeout(resolve, intervalMs));
		const next = runtime
			? await apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target: "tmux" } } })
			: await apiClient.GET("/api/v1/agents/{agent}/install", { params: { path: { agent } } });
		// A failed status read is transient; keep the last job and poll again.
		if (next.data) job = next.data;
	}
	return job;
}
