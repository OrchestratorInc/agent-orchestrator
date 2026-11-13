import type { DashboardSession, OrchestratorLink, SessionMode } from "../api";
import { cloudAgentLabel, cloudTaskTitle, defaultCloudAgent } from "./agents";
import type { CloudClient } from "./client";
import { CloudError, cloudErrorMessage } from "./errors";
import { cloudOrchestratorLink, cloudSessionRow } from "./mapping";
import type { CloudApprovalMode, CloudOrganization, CloudProject, CloudProviderConnection, CloudSession, CreateCloudSessionInput } from "./types";

// The store's board actions (spawn, archive, restore, launch an orchestrator…)
// for rows that live in Orchestrator Cloud. The store calls these when an action's host
// is CLOUD_HOST_ID, so the Workers board, the Projects tab and the spawn sheet
// drive cloud sessions through the same handlers they use for a machine.

export type CloudActionContext = {
	client: CloudClient;
	org: CloudOrganization | null;
	projects: readonly CloudProject[];
	connections: readonly CloudProviderConnection[];
	chatHarnesses: readonly string[];
	/** The listed sessions, so a pin moves its row before the server answers. */
	sessions?: readonly CloudSession[];
	upsertSession(session: CloudSession): void;
	refreshSessions(): Promise<void>;
};

/**
 * The board's screens describe a failure with userFacingError, which reads any
 * HTTP status as the paired machine answering ("re-scan the pairing code").
 * A control-plane failure is not that, so it leaves here as plain words.
 */
async function inCloudWords<T>(work: () => Promise<T>): Promise<T> {
	try {
		return await work();
	} catch (cause) {
		if (cause instanceof CloudError) throw Object.assign(new Error(cloudErrorMessage(cause)), { cloudCode: cause.code, cause });
		throw cause;
	}
}

/** What retitling or pinning a session needs: the screen's cloud state is enough. */
type SessionEditContext = Pick<CloudActionContext, "client" | "org" | "sessions" | "upsertSession" | "refreshSessions">;

function orgOf(context: Pick<CloudActionContext, "org">): string {
	if (!context.org) throw new Error("Sign in to Orchestrator Cloud first.");
	return context.org.id;
}

export type CloudSpawnRequest = {
	projectId?: string;
	prompt?: string;
	harness?: string;
	model?: string;
	/** The model's effort level; omitted runs the model's default. */
	effort?: string;
	/** The opening turn's approval mode; omitted lets the worker derive it. */
	approvalMode?: CloudApprovalMode;
	mode?: SessionMode;
	attachments?: readonly unknown[];
	clientRequestId?: string;
};

/**
 * Start a cloud worker the way the desktop's task composer does: the prompt's
 * first line names it, Chat where the harness supports it. A retry with the
 * same request id is the same request (Idempotency-Key), so a dropped response
 * never starts a second sandbox.
 */
export function spawnCloudWorker(context: CloudActionContext, request: CloudSpawnRequest): Promise<DashboardSession> {
	return inCloudWords(() => spawnWorker(context, request));
}

async function spawnWorker(context: CloudActionContext, request: CloudSpawnRequest): Promise<DashboardSession> {
	const orgId = orgOf(context);
	if (!request.projectId) throw new Error("Pick a cloud project first.");
	if (request.attachments?.length) throw new Error("Cloud tasks can't take attachments yet. Remove them, or describe the files in the prompt.");
	const harness = request.harness || defaultCloudAgent(context.connections, context.projects.find((item) => item.id === request.projectId)?.config?.worker?.agent);
	if (!harness) throw new Error("Connect a coding agent to Orchestrator Cloud first (Settings → Coding agents).");
	const prompt = request.prompt?.trim() ?? "";
	const wantsChat = (request.mode ?? "chat") === "chat" && context.chatHarnesses.includes(harness);
	const input: CreateCloudSessionInput = {
		projectId: request.projectId,
		kind: "worker",
		harness,
		displayName: cloudTaskTitle(prompt, cloudAgentLabel(harness)),
		prompt,
		...(request.model ? { model: request.model } : {}),
		...(request.effort ? { reasoningEffort: request.effort } : {}),
		// The approval mode governs a chat turn; a terminal session has none to run.
		...(request.approvalMode && wantsChat ? { approvalMode: request.approvalMode } : {}),
		interfaceMode: wantsChat ? "chat" : "tui",
	};
	// The sheet's request id makes a retried tap the same request; the client mints one otherwise.
	const key = request.clientRequestId;
	let created: { session: CloudSession };
	try {
		created = await context.client.createSession(orgId, input, key);
	} catch (cause) {
		// An older control plane refuses interfaceMode; it then starts the harness's default.
		if (!(cause instanceof CloudError && cause.status === 400 && cause.code === "invalid_request")) throw cause;
		const { interfaceMode: _omitted, ...legacy } = input;
		created = await context.client.createSession(orgId, legacy, key ? `${key}:legacy` : undefined);
	}
	context.upsertSession(created.session);
	void context.refreshSessions();
	return cloudSessionRow(created.session);
}

/** Start a project's cloud orchestrator with its configured agent, else the first connected one. */
export function launchCloudOrchestrator(context: CloudActionContext, projectId: string, mode: SessionMode = "chat"): Promise<OrchestratorLink> {
	return inCloudWords(() => launchOrchestrator(context, projectId, mode));
}

async function launchOrchestrator(context: CloudActionContext, projectId: string, mode: SessionMode): Promise<OrchestratorLink> {
	const orgId = orgOf(context);
	const project = context.projects.find((item) => item.id === projectId);
	const harness = defaultCloudAgent(context.connections, project?.config?.orchestrator?.agent);
	if (!harness) throw new Error("Connect a coding agent to Orchestrator Cloud first (Settings → Coding agents).");
	const { session } = await context.client.createSession(orgId, {
		projectId, kind: "orchestrator", harness, displayName: "Orchestrator", prompt: "",
		interfaceMode: mode === "chat" && context.chatHarnesses.includes(harness) ? "chat" : "tui",
	});
	context.upsertSession(session);
	void context.refreshSessions();
	return cloudOrchestratorLink(session, project?.displayName ?? "Project");
}

/** Archive: the sandbox stops; the conversation and branch are kept. */
export function archiveCloudSession(context: CloudActionContext, id: string): Promise<void> {
	return inCloudWords(async () => {
		await context.client.deleteSession(orgOf(context), id);
		await context.refreshSessions();
	});
}

export function restoreCloudSession(context: CloudActionContext, id: string): Promise<void> {
	return inCloudWords(async () => {
		await context.client.restoreSession(orgOf(context), id);
		await context.refreshSessions();
	});
}

/**
 * Resume agent, for a live session whose agent exited or crashed: start it
 * again in its sandbox, as the desktop's ResumeAgentControl does. Waking the
 * sandbox comes first, since the worker that restarts the agent has to be up
 * (a no-op when it already is, as on every chat open).
 */
export function resumeCloudAgent(context: CloudActionContext, id: string): Promise<void> {
	return inCloudWords(async () => {
		await context.client.resumeSession(orgOf(context), id);
		const { requested } = await context.client.restartSessionAgent(orgOf(context), id);
		await context.refreshSessions();
		if (!requested) throw new Error("The agent is already running.");
	});
}

export function sendCloudMessage(context: CloudActionContext, id: string, text: string): Promise<void> {
	return inCloudWords(async () => {
		await context.client.sendMessage(orgOf(context), id, { text });
	});
}

/** Retitle a cloud session: the name every viewer of it sees. */
export function renameCloudSession(context: SessionEditContext, id: string, displayName: string): Promise<void> {
	return inCloudWords(async () => {
		const { session } = await context.client.renameSession(orgOf(context), id, displayName);
		if (session) context.upsertSession(session);
		else await context.refreshSessions();
	});
}

/**
 * Pin or unpin a cloud session for the signed-in person (nobody else sees a
 * pin). The row moves at once and moves back if the control plane refuses.
 */
export function setCloudSessionPinned(context: SessionEditContext, id: string, pinned: boolean): Promise<void> {
	return inCloudWords(async () => {
		const orgId = orgOf(context);
		const before = context.sessions?.find((item) => item.id === id);
		if (before) context.upsertSession({ ...before, pinned, pinnedAt: pinned ? before.pinnedAt ?? new Date().toISOString() : undefined });
		try {
			const result = await (pinned ? context.client.pinSession(orgId, id) : context.client.unpinSession(orgId, id));
			if (before) context.upsertSession({ ...before, pinned: result.pinned, pinnedAt: result.pinned ? result.pinnedAt : undefined });
		} catch (cause) {
			if (before) context.upsertSession(before);
			throw cause;
		}
	});
}

/** Board actions Orchestrator Cloud has no route for yet. */
export function unsupportedCloudAction(action: "merge"): never {
	throw new Error(action === "merge" ? "Merge cloud pull requests on GitHub for now." : "Not available for cloud sessions yet.");
}
