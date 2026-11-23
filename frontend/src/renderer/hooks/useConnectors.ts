import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";

export type Connector = components["schemas"]["ConnectorResponse"];
export type ConnectorInput = components["schemas"]["ConnectorRequest"];
export type ConnectorImportCandidate = components["schemas"]["ConnectorImportCandidateResponse"];
export type ConnectorSignIn = components["schemas"]["ConnectorSignInResponse"];
export type SessionConnector = components["schemas"]["SessionConnectorResponse"];
export type ConnectorProposal = components["schemas"]["ConnectorProposalResponse"];

export const connectorsQueryKey = ["connectors"] as const;
export const connectorImportQueryKey = ["connectors", "import"] as const;
export const sessionConnectorsQueryKey = (sessionId: string, hostId?: string) => ["connectors", "session", hostId ?? "local", sessionId] as const;
export const sessionProposalsQueryKey = (sessionId: string, hostId?: string) => ["connectors", "proposals", hostId ?? "local", sessionId] as const;

const previewStamp = new Date(0).toISOString();

const previewConnectors: Connector[] = [
	{ id: "p1", key: "linear", name: "Linear", transport: "http", url: "https://mcp.linear.app/mcp", args: [], envKeys: [], headerKeys: [], auth: "oauth", signedIn: true, enabled: true, status: "connected", tools: ["list_issues", "create_issue", "update_issue"], createdAt: previewStamp, updatedAt: previewStamp },
	{ id: "p2", key: "sentry", name: "Sentry", transport: "http", url: "https://mcp.sentry.dev/mcp", args: [], envKeys: [], headerKeys: [], auth: "oauth", signedIn: false, enabled: true, status: "needs_signin", tools: [], createdAt: previewStamp, updatedAt: previewStamp },
	{ id: "p3", key: "files", name: "files", transport: "stdio", command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem", "~/notes"], envKeys: [], headerKeys: [], auth: "none", signedIn: false, enabled: false, status: "unchecked", tools: [], createdAt: previewStamp, updatedAt: previewStamp },
];

async function one(result: Promise<{ data?: { connector: Connector }; error?: unknown }>): Promise<Connector> {
	const { data, error } = await result;
	if (error || !data) throw apiErrorException(error);
	return data.connector;
}

export const connectorsApi = {
	async list(): Promise<Connector[]> {
		if (usesPreviewWorkspaceData) return previewConnectors;
		const { data, error } = await apiClient.GET("/api/v1/connectors");
		if (error || !data) throw apiErrorException(error);
		return data.connectors;
	},
	get: (connectorId: string, hostId?: string) =>
		one(clientForSessionHost(hostId).GET("/api/v1/connectors/{connectorId}", { params: { path: { connectorId } } })),
	create: (body: ConnectorInput) => one(apiClient.POST("/api/v1/connectors", { body })),
	update: (connectorId: string, body: ConnectorInput) =>
		one(apiClient.PATCH("/api/v1/connectors/{connectorId}", { params: { path: { connectorId } }, body })),
	test: (connectorId: string) => one(apiClient.POST("/api/v1/connectors/{connectorId}/test", { params: { path: { connectorId } } })),
	signOut: (connectorId: string) =>
		one(apiClient.POST("/api/v1/connectors/{connectorId}/disconnect", { params: { path: { connectorId } } })),
	finishSignIn: (connectorId: string, callbackUrl: string, hostId?: string) =>
		one(clientForSessionHost(hostId).POST("/api/v1/connectors/{connectorId}/connect/finish", { params: { path: { connectorId } }, body: { callbackUrl } })),
	async remove(connectorId: string): Promise<void> {
		const { error } = await apiClient.DELETE("/api/v1/connectors/{connectorId}", { params: { path: { connectorId } } });
		if (error) throw apiErrorException(error);
	},
	async startSignIn(connectorId: string, hostId?: string): Promise<ConnectorSignIn> {
		if (usesPreviewWorkspaceData) {
			const connector = previewConnectors.find((one) => one.id === connectorId) ?? (previewConnectors[0] as Connector);
			return { authorizationUrl: "https://mcp.example.com/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector };
		}
		const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/connectors/{connectorId}/connect", { params: { path: { connectorId } } });
		if (error || !data) throw apiErrorException(error);
		return data;
	},
	async importCandidates(): Promise<ConnectorImportCandidate[]> {
		if (usesPreviewWorkspaceData) return [];
		const { data, error } = await apiClient.GET("/api/v1/connectors/import");
		if (error || !data) throw apiErrorException(error);
		return data.candidates;
	},
	async importChosen(ids: string[], values: Record<string, Record<string, string>>): Promise<Connector[]> {
		const { data, error } = await apiClient.POST("/api/v1/connectors/import", { body: { ids, values } });
		if (error || !data) throw apiErrorException(error);
		return data.connectors;
	},
	async sessionProposals(sessionId: string, hostId?: string): Promise<ConnectorProposal[]> {
		if (usesPreviewWorkspaceData) return [];
		const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/connectors/proposals", { params: { path: { sessionId } } });
		if (error || !data) throw apiErrorException(error);
		return data.proposals;
	},
	async approveProposal(proposalId: string, body: { projectId?: string; values?: Record<string, string> }, hostId?: string): Promise<Connector> {
		const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/connectors/proposals/{proposalId}/approve", { params: { path: { proposalId } }, body });
		if (error || !data) throw apiErrorException(error);
		return data.connector;
	},
	async declineProposal(proposalId: string, hostId?: string): Promise<void> {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/connectors/proposals/{proposalId}/decline", { params: { path: { proposalId } } });
		if (error) throw apiErrorException(error);
	},
	async forSession(sessionId: string, hostId?: string): Promise<SessionConnector[]> {
		if (usesPreviewWorkspaceData) return [];
		const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/connectors", { params: { path: { sessionId } } });
		if (error || !data) throw apiErrorException(error);
		return data.connectors;
	},
};

export function useConnectors() {
	return useQuery({ queryKey: connectorsQueryKey, queryFn: connectorsApi.list, refetchInterval: 3000 });
}

export function splitCommandLine(line: string): string[] {
	const out: string[] = [];
	let word = "";
	let quote = "";
	let open = false;
	for (let i = 0; i < line.length; i += 1) {
		const ch = line[i] as string;
		const next = line[i + 1];
		if (quote) {
			if (ch === quote) quote = "";
			else if (quote === '"' && ch === "\\" && (next === '"' || next === "\\")) {
				word += next;
				i += 1;
			} else word += ch;
		} else if (ch === '"' || ch === "'") {
			quote = ch;
			open = true;
		} else if (ch === "\\" && next !== undefined && /[\s"'\\]/.test(next)) {
			word += next;
			open = true;
			i += 1;
		} else if (/\s/.test(ch)) {
			if (open || word) out.push(word);
			word = "";
			open = false;
		} else {
			word += ch;
		}
	}
	if (open || word) out.push(word);
	return out;
}

export function joinCommandLine(words: readonly string[]): string {
	return words
		.map((word) => (word !== "" && !/[\s"'\\]/.test(word) ? word : `"${word.replace(/[\\"]/g, "\\$&")}"`))
		.join(" ");
}

export function connectorKeyFrom(name: string): string {
	return name.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32);
}
