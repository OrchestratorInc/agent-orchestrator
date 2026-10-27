import { cloudCpFetch } from "./cloud-cp/renderer-client";

export type AccountRemoteHost = { hostId: string; label: string; url: string; token: string };

/** AO Cloud refused a new machine because the account lists as many as it can. */
export class RemoteHostLimitError extends Error {}

async function request(baseUrl: string, method: string, path: string, body?: unknown): Promise<Response> {
	const response = await cloudCpFetch(`${baseUrl.replace(/\/+$/, "")}/api/cloud/v1/me/hosts${path}`, {
		method,
		headers: { Authorization: "Bearer delegated-to-main-process", ...(body ? { "Content-Type": "application/json" } : {}) },
		body: body ? JSON.stringify(body) : undefined,
		cache: "no-store",
		signal: AbortSignal.timeout(10_000),
	});
	if (!response.ok) {
		// A full account is the one refusal the user can act on, so its message is shown.
		const refusal = (await response.json().catch(() => null)) as { code?: string; message?: string } | null;
		if (refusal?.code === "remote_host_limit_reached" && refusal.message) throw new RemoteHostLimitError(refusal.message);
		throw new Error(`AO Cloud host sync failed (${response.status}).`);
	}
	return response;
}

export async function listAccountRemoteHosts(baseUrl: string): Promise<AccountRemoteHost[]> {
	const body = await (await request(baseUrl, "GET", "")).json() as { hosts?: AccountRemoteHost[] };
	return body.hosts ?? [];
}

/** A host saved without a url is listed at its fixed address: AO Cloud knows it from the host id. */
export async function saveAccountRemoteHost(baseUrl: string, host: Omit<AccountRemoteHost, "url"> & { url?: string }): Promise<void> {
	await request(baseUrl, "PUT", `/${encodeURIComponent(host.hostId)}`, {
		label: host.label, url: host.url, token: host.token,
	});
}

export async function deleteAccountRemoteHost(baseUrl: string, hostId: string): Promise<void> {
	await request(baseUrl, "DELETE", `/${encodeURIComponent(hostId)}`);
}
