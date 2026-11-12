import type { SettingsTone } from "../SettingsForm.types";
import { agentConnectionState, CLOUD_AGENT_LABELS, CLOUD_AGENTS } from "./agents";
import type { CloudState } from "./CloudProvider";

/**
 * Orchestrator Cloud's line in Settings' machine list, said the way a paired
 * machine's is: whether it answers, then the one thing worth knowing about it,
 * which is whether a coding agent can run there. A credential the provider
 * stopped accepting outranks the count, since nothing starts until it is fixed.
 */
export function cloudSettingsStatus(cloud: Pick<CloudState, "status" | "connections" | "connectionsLoaded">): { label: string; tone: SettingsTone } {
	if (cloud.status === "error") return { label: "Can't reach Orchestrator Cloud", tone: "bad" };
	if (cloud.status !== "ready") return { label: "Connecting…", tone: "default" };
	if (!cloud.connectionsLoaded) return { label: "Connected", tone: "good" };
	const invalid = CLOUD_AGENTS.find((agent) => agentConnectionState(cloud.connections, agent) === "invalid");
	if (invalid) return { label: `Sign in to ${CLOUD_AGENT_LABELS[invalid]} again`, tone: "bad" };
	const connected = CLOUD_AGENTS.filter((agent) => agentConnectionState(cloud.connections, agent) === "connected").length;
	if (connected === 0) return { label: "No coding agent connected", tone: "default" };
	return { label: `Connected · ${connected} coding ${connected === 1 ? "agent" : "agents"}`, tone: "good" };
}
