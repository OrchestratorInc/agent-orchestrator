import { agentLabel } from "./agent-options";
import type { AgentInfo } from "./agent-select-options";
import type { CloudCpProviderConnection } from "./cloud-cp";

export const CLOUD_AGENT_PROVIDERS = ["claude-code", "codex", "cursor", "opencode"] as const;

const CREDENTIAL_SCOPE_PREFIX = "@cred:";

export function credentialModelScope(credentialType: string): string {
	return `${CREDENTIAL_SCOPE_PREFIX}${credentialType}`;
}

export function modelCatalogScope(scope: string): string {
	return scope.startsWith(CREDENTIAL_SCOPE_PREFIX) && scope.slice(CREDENTIAL_SCOPE_PREFIX.length).trim() !== "" ? scope : "";
}

export function connectedCredentialType(
	connections: CloudCpProviderConnection[] | undefined,
	provider: string,
): string {
	const forProvider = (connections ?? []).filter(
		(connection) => connection.provider === provider && connection.validationState === "valid",
	);
	const chosen = forProvider.find((connection) => connection.label === "default") ?? forProvider[0];
	const credentialType = chosen?.config?.credentialType;
	return typeof credentialType === "string" ? credentialType : "";
}

export function isCloudHarnessConnected(connections: CloudCpProviderConnection[] | undefined, provider: string): boolean {
	return (connections ?? []).some(
		(connection) => connection.provider === provider && connection.label === "default" && connection.validationState === "valid",
	);
}

export function cloudAgentInfos(connections: CloudCpProviderConnection[] | undefined): AgentInfo[] {
	return CLOUD_AGENT_PROVIDERS.map((id) => {
		const authorized = isCloudHarnessConnected(connections, id);
		return {
			id,
			label: agentLabel(id),
			installation: { state: "installed", freshness: "fresh" },
			authentication: { state: authorized ? "authorized" : "unauthorized", freshness: "fresh" },
			effectiveReadiness: authorized ? "ready" : "not_ready",
			usageCount: 0,
			lastUsedAt: null,
		};
	});
}
