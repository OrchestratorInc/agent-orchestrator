import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";

export type Connector = components["schemas"]["ConnectorResponse"];
export type ConnectorInput = components["schemas"]["ConnectorRequest"];
export type ConnectorImportCandidate = components["schemas"]["ConnectorImportCandidateResponse"];
export type ConnectorSignIn = components["schemas"]["ConnectorSignInResponse"];
export type SessionConnector = components["schemas"]["SessionConnectorResponse"];
export type ConnectorProposal = components["schemas"]["ConnectorProposalResponse"];

export const connectorsQueryKey = ["connectors"] as const;
export const connectorImportQueryKey = ["connectors", "import"] as const;
export const sessionConnectorsQueryKey = (sessionId: string) => ["connectors", "session", sessionId] as const;
export const sessionProposalsQueryKey = (sessionId: string) => ["connectors", "proposals", sessionId] as const;

const previewStamp = new Date(0).toISOString();

// The web preview build has no daemon, so it shows a fixed list.
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
	create: (body: ConnectorInput) => one(apiClient.POST("/api/v1/connectors", { body })),
	update: (connectorId: string, body: ConnectorInput) =>
		one(apiClient.PATCH("/api/v1/connectors/{connectorId}", { params: { path: { connectorId } }, body })),
	test: (connectorId: string) => one(apiClient.POST("/api/v1/connectors/{connectorId}/test", { params: { path: { connectorId } } })),
	signOut: (connectorId: string) =>
		one(apiClient.POST("/api/v1/connectors/{connectorId}/disconnect", { params: { path: { connectorId } } })),
	finishSignIn: (connectorId: string, callbackUrl: string) =>
		one(apiClient.POST("/api/v1/connectors/{connectorId}/connect/finish", { params: { path: { connectorId } }, body: { callbackUrl } })),
	async remove(connectorId: string): Promise<void> {
		const { error } = await apiClient.DELETE("/api/v1/connectors/{connectorId}", { params: { path: { connectorId } } });
		if (error) throw apiErrorException(error);
	},
	async startSignIn(connectorId: string): Promise<ConnectorSignIn> {
		if (usesPreviewWorkspaceData) {
			const connector = previewConnectors.find((one) => one.id === connectorId) ?? (previewConnectors[0] as Connector);
			return { authorizationUrl: "https://mcp.example.com/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector };
		}
		const { data, error } = await apiClient.POST("/api/v1/connectors/{connectorId}/connect", { params: { path: { connectorId } } });
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
	async sessionProposals(sessionId: string): Promise<ConnectorProposal[]> {
		if (usesPreviewWorkspaceData) return [];
		const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/connectors/proposals", { params: { path: { sessionId } } });
		if (error || !data) throw apiErrorException(error);
		return data.proposals;
	},
	async approveProposal(proposalId: string, body: { projectId?: string; values?: Record<string, string> }): Promise<Connector> {
		const { data, error } = await apiClient.POST("/api/v1/connectors/proposals/{proposalId}/approve", { params: { path: { proposalId } }, body });
		if (error || !data) throw apiErrorException(error);
		return data.connector;
	},
	async declineProposal(proposalId: string): Promise<void> {
		const { error } = await apiClient.POST("/api/v1/connectors/proposals/{proposalId}/decline", { params: { path: { proposalId } } });
		if (error) throw apiErrorException(error);
	},
	async forSession(sessionId: string): Promise<SessionConnector[]> {
		if (usesPreviewWorkspaceData) return [];
		const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/connectors", { params: { path: { sessionId } } });
		if (error || !data) throw apiErrorException(error);
		return data.connectors;
	},
};

/** The connector list. It refetches while a consumer is mounted, so a status a call or a sign-in changed shows without a reload. */
export function useConnectors() {
	return useQuery({ queryKey: connectorsQueryKey, queryFn: connectorsApi.list, refetchInterval: 3000 });
}

/**
 * Splits a command line into its program and arguments. Quotes group words.
 * A backslash keeps the next character outside quotes, and keeps a quote or a
 * backslash inside double quotes. Single quotes keep everything as it is.
 */
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
		} else if (ch === "\\" && next !== undefined) {
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

/** Writes a program and its arguments as one line that splitCommandLine reads back unchanged. */
export function joinCommandLine(words: readonly string[]): string {
	return words
		.map((word) => (word !== "" && !/[\s"'\\]/.test(word) ? word : `"${word.replace(/[\\"]/g, "\\$&")}"`))
		.join(" ");
}

/** The key a connector gets from a name: lowercase letters, digits, hyphens and underscores, 32 at most. */
export function connectorKeyFrom(name: string): string {
	return name.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32);
}
