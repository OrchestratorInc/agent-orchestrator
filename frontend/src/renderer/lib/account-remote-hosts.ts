import { cloudCpFetch } from "./cloud-cp/renderer-client";

export type AccountRemoteHost = { hostId: string; label: string; url: string; token: string };

async function request(baseUrl: string, method: string, path: string, body?: unknown): Promise<Response> {
	const response = await cloudCpFetch(`${baseUrl.replace(/\/+$/, "")}/api/cloud/v1/me/hosts${path}`, {
		method,
		headers: { Authorization: "Bearer delegated-to-main-process", ...(body ? { "Content-Type": "application/json" } : {}) },
		body: body ? JSON.stringify(body) : undefined,
		cache: "no-store",
		signal: AbortSignal.timeout(10_000),
	});
	if (!response.ok) throw new Error(`AO Cloud host sync failed (${response.status}).`);
	return response;
}

export async function listAccountRemoteHosts(baseUrl: string): Promise<AccountRemoteHost[]> {
	const body = await (await request(baseUrl, "GET", "")).json() as { hosts?: AccountRemoteHost[] };
	return body.hosts ?? [];
}

export async function saveAccountRemoteHost(baseUrl: string, host: AccountRemoteHost): Promise<void> {
	await request(baseUrl, "PUT", `/${encodeURIComponent(host.hostId)}`, {
		label: host.label, url: host.url, token: host.token,
	});
}

export async function deleteAccountRemoteHost(baseUrl: string, hostId: string): Promise<void> {
	await request(baseUrl, "DELETE", `/${encodeURIComponent(hostId)}`);
}
