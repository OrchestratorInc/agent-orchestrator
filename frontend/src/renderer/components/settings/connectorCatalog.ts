export type ConnectorShelf = "popular" | "developer" | "productivity";

export type ConnectorPresetId =
	| "linear" | "notion" | "supabase" | "vercel" | "stripe" | "sentry"
	| "posthog" | "neon" | "context7" | "atlassian" | "canva" | "monday";

export type ConnectorPreset = {
	id: ConnectorPresetId;
	name: string;
	url: string;
	auth: "oauth" | "none";
	shelf: ConnectorShelf;
};

export const connectorShelves: readonly ConnectorShelf[] = ["popular", "developer", "productivity"];

export const connectorPresets: readonly ConnectorPreset[] = [
	{ id: "linear", name: "Linear", url: "https://mcp.linear.app/mcp", auth: "oauth", shelf: "popular" },
	{ id: "notion", name: "Notion", url: "https://mcp.notion.com/mcp", auth: "oauth", shelf: "popular" },
	{ id: "supabase", name: "Supabase", url: "https://mcp.supabase.com/mcp", auth: "oauth", shelf: "popular" },
	{ id: "vercel", name: "Vercel", url: "https://mcp.vercel.com", auth: "oauth", shelf: "popular" },
	{ id: "stripe", name: "Stripe", url: "https://mcp.stripe.com", auth: "oauth", shelf: "popular" },
	{ id: "sentry", name: "Sentry", url: "https://mcp.sentry.dev/mcp", auth: "oauth", shelf: "developer" },
	{ id: "posthog", name: "PostHog", url: "https://mcp.posthog.com/mcp", auth: "oauth", shelf: "developer" },
	{ id: "neon", name: "Neon", url: "https://mcp.neon.tech/mcp", auth: "oauth", shelf: "developer" },
	{ id: "context7", name: "Context7", url: "https://mcp.context7.com/mcp", auth: "none", shelf: "developer" },
	{ id: "atlassian", name: "Atlassian", url: "https://mcp.atlassian.com/v1/mcp", auth: "oauth", shelf: "productivity" },
	{ id: "canva", name: "Canva", url: "https://mcp.canva.com/mcp", auth: "oauth", shelf: "productivity" },
	{ id: "monday", name: "monday.com", url: "https://mcp.monday.com/mcp", auth: "oauth", shelf: "productivity" },
];
