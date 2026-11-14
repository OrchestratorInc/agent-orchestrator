import { apiErrorException } from "./api-error-exception";
import { apiErrorCode, apiErrorDetails, apiErrorMessage, apiErrorRequestId } from "./api-client";
import { appI18n } from "../i18n";
import { aoBridge } from "./bridge";
import { resumeOnce } from "../hooks/useCanResumeAgent";
import type { OrchestratorSpawnSource } from "./orchestrator-spawn-sources";
import { captureRendererEvent } from "./telemetry";
import { clientForSessionHost } from "./host-clients";
import type { SessionMode } from "../types/conversation";

export type { OrchestratorSpawnSource };

const CHAT_PREFLIGHT_CODES = new Set([
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_DRIVER_UNAVAILABLE",
	"CHAT_DRIVER_INCOMPATIBLE",
	"CHAT_AUTH_REQUIRED",
]);

export class OrchestratorSpawnError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly requestId?: string,
		readonly status?: number,
		readonly details?: Record<string, unknown>,
	) {
		super(message);
		this.name = "OrchestratorSpawnError";
	}
}

export function isChatPreflightCode(code?: string): boolean {
	return Boolean(code && CHAT_PREFLIGHT_CODES.has(code));
}

export function canBypassOrchestratorApprovals(code?: string, details?: Record<string, unknown>): boolean {
	if (code !== "SESSION_MODE_UNSUPPORTED" || !details) return false;
	const has = (key: string, value: string) => {
		const entry = details[key];
		return entry === value || (Array.isArray(entry) && entry.includes(value));
	};
	return has("missingCapabilities", "approvals") && has("allowedApprovalModes", "bypass-permissions");
}

export function isChatPreflightError(error: unknown): error is OrchestratorSpawnError {
	return error instanceof OrchestratorSpawnError && isChatPreflightCode(error.code);
}

export async function spawnOrchestrator(
	projectId: string,
	source: OrchestratorSpawnSource,
	clean = false,
	mode?: SessionMode,
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions",
	hostId?: string,
): Promise<string> {
	try {
		const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/orchestrators", {
			body: { projectId, clean, ...(mode ? { mode } : {}), ...(approvalMode ? { approvalMode } : {}) },
		});

		if (error || !data?.orchestrator?.id) {
			const message = error
				? apiErrorMessage(error, `Failed to spawn orchestrator (${response.status})`)
				: `Failed to spawn orchestrator (${response.status})`;
			throw new OrchestratorSpawnError(
				message,
				apiErrorCode(error),
				apiErrorRequestId(error),
				response.status,
				apiErrorDetails(error),
			);
		}

		void captureRendererEvent("ao.orchestrators.spawned", { project_id: projectId, source });
		return data.orchestrator.id;
	} catch (err) {
		void captureRendererEvent("ao.orchestrators.spawn_failed", {
			project_id: projectId, source,
			...(err instanceof OrchestratorSpawnError ? { error_code: err.code, http_status: err.status } : {}),
		});
		throw err;
	}
}

export function resumeOrchestrator(sessionId: string, hostId?: string): Promise<void> {
	return hostId ? requestOrchestratorResume(sessionId, hostId) : resumeOnce(sessionId, () => requestOrchestratorResume(sessionId));
}

async function requestOrchestratorResume(sessionId: string, hostId?: string): Promise<void> {
	const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/resume-agent", {
		params: { path: { sessionId } },
	});
	if (error && apiErrorCode(error) !== "AGENT_NOT_EXITED") {
		throw apiErrorException(error, `Could not resume the orchestrator (${response.status})`);
	}
	if (data?.resumeMode === "saved_prompt") {
		void aoBridge.notifications
			.show({
				id: `resume-agent-fallback:${sessionId}:${Date.now()}`,
				title: appI18n.t("inspector.startedFromPrompt"),
				body: appI18n.t("inspector.resumeFallbackBody"),
			})
			.catch((err) => console.warn("Unable to show resume fallback notification", err));
	}
}
