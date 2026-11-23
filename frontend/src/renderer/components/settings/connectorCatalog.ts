/** A server the add dialog can fill the form with. */
export type ConnectorPreset = {
	id: string;
	name: string;
	url: string;
	auth: "oauth" | "none";
};

/**
 * Servers whose sign-in a connector can do by itself: each answers an
 * unauthenticated request with a sign-in challenge, lets a client register
 * itself, and supports PKCE. Context7 needs no sign-in.
 */
export const connectorPresets: readonly ConnectorPreset[] = [
	{ id: "linear", name: "Linear", url: "https://mcp.linear.app/mcp", auth: "oauth" },
	{ id: "notion", name: "Notion", url: "https://mcp.notion.com/mcp", auth: "oauth" },
	{ id: "sentry", name: "Sentry", url: "https://mcp.sentry.dev/mcp", auth: "oauth" },
	{ id: "supabase", name: "Supabase", url: "https://mcp.supabase.com/mcp", auth: "oauth" },
	{ id: "vercel", name: "Vercel", url: "https://mcp.vercel.com", auth: "oauth" },
	{ id: "posthog", name: "PostHog", url: "https://mcp.posthog.com/mcp", auth: "oauth" },
	{ id: "stripe", name: "Stripe", url: "https://mcp.stripe.com", auth: "oauth" },
	{ id: "neon", name: "Neon", url: "https://mcp.neon.tech/mcp", auth: "oauth" },
	{ id: "atlassian", name: "Atlassian", url: "https://mcp.atlassian.com/v1/mcp", auth: "oauth" },
	{ id: "canva", name: "Canva", url: "https://mcp.canva.com/mcp", auth: "oauth" },
	{ id: "monday", name: "monday.com", url: "https://mcp.monday.com/mcp", auth: "oauth" },
	{ id: "context7", name: "Context7", url: "https://mcp.context7.com/mcp", auth: "none" },
];
