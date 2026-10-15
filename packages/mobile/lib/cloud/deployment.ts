export interface CloudDeployment {
	baseUrl: string;
	clientId: string;
	redirectUri: string;
	/** Native URL delivered by the hosted return page (or the direct redirect). */
	callbackUri: string;
	authKind: "workos" | "local";
	key: string;
}

export function resolveCloudDeployment(input: {
	cloudUrl?: string; accountUrl?: string; clientId?: string; redirectUri?: string; development: boolean;
}): CloudDeployment {
	const normalize = (raw: string): string => {
		const url = new URL(raw.trim());
		const loopback = ["localhost", "127.0.0.1", "[::1]", "10.0.2.2"].includes(url.hostname);
		if (url.username || url.password || url.search || url.hash ||
			(url.protocol !== "https:" && !(input.development && loopback && url.protocol === "http:"))) {
			throw new Error("Cloud API URL must use HTTPS and contain no credentials, query, or fragment.");
		}
		return url.toString().replace(/\/+$/, "");
	};
	const cloud = input.cloudUrl?.trim() ? normalize(input.cloudUrl) : undefined;
	const account = input.accountUrl?.trim() ? normalize(input.accountUrl) : undefined;
	if (cloud && account && cloud !== account) throw new Error("Cloud deployment settings disagree. Use the same API for account and workspace.");
	const baseUrl = cloud ?? account ?? (input.development ? "https://staging-api.aoagents.dev" : "https://api.aoagents.dev");
	const production = baseUrl === "https://api.aoagents.dev";
	const clientId = input.clientId?.trim() || (production ? "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X" : "client_01KZ3VRKC374HS91XGRDPT3671");
	// Match desktop's registered production redirect, which bounces to ao-app.
	// Staging and custom deployments retain their existing direct native return.
	const redirectUri = input.redirectUri?.trim() || (production ? `${baseUrl}/app/auth/return` : "aoagents.ao://callback");
	const callbackUri = redirectUri === "https://api.aoagents.dev/app/auth/return" ? "ao-app://callback" : redirectUri;
	const authKind = baseUrl.startsWith("http:") ? "local" : "workos";
	// Preserve the existing credential scope for unchanged direct-return logins.
	const key = JSON.stringify([baseUrl, clientId, redirectUri, authKind, ...(callbackUri !== redirectUri ? [callbackUri] : [])]);
	return { baseUrl, clientId, redirectUri, callbackUri, authKind, key };
}

export function cloudAccountScopeKey(input: { baseUrl: string; userId: string; orgId: string }): string {
	return JSON.stringify([input.baseUrl.replace(/\/+$/, ""), input.userId, input.orgId]);
}
