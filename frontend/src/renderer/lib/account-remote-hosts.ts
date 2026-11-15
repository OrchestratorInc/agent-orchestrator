import { cloudCpFetch } from "./cloud-cp/renderer-client";

export type AccountRemoteHost = { hostId: string; label: string; url: string };

export class AccountHostSyncError extends Error {
	constructor(message: string, readonly status: number) {
		super(message);
	}
}

async function request(baseUrl: string, method: string, path: string, body?: unknown, timeoutMs: number | null = 10_000): Promise<Response> {
	const response = await cloudCpFetch(`${baseUrl.replace(/\/+$/, "")}/api/cloud/v1/me/hosts${path}`, {
		method,
		headers: { Authorization: "Bearer delegated-to-main-process", ...(body ? { "Content-Type": "application/json" } : {}) },
		body: body ? JSON.stringify(body) : undefined,
		cache: "no-store",
		signal: timeoutMs === null ? undefined : AbortSignal.timeout(timeoutMs),
	});
	if (!response.ok) throw new AccountHostSyncError(`Orchestrator Cloud host sync failed (${response.status}).`, response.status);
	return response;
}

export type AccountHostDirectory = { hosts: AccountRemoteHost[]; fixedAddresses: boolean; hostKeys: boolean };

export async function readAccountHostDirectory(baseUrl: string): Promise<AccountHostDirectory> {
	const body = await (await request(baseUrl, "GET", "")).json() as Partial<AccountHostDirectory>;
	return { hosts: body.hosts ?? [], fixedAddresses: body.fixedAddresses === true, hostKeys: body.hostKeys === true };
}

export async function listAccountRemoteHosts(baseUrl: string): Promise<AccountRemoteHost[]> {
	return (await readAccountHostDirectory(baseUrl)).hosts;
}

export async function saveAccountRemoteHost(baseUrl: string, host: Omit<AccountRemoteHost, "url"> & { url?: string; token: string; hostKey?: string }): Promise<void> {
	await request(baseUrl, "PUT", `/${encodeURIComponent(host.hostId)}`, {
		label: host.label, url: host.url, token: host.token, hostKey: host.hostKey || undefined,
	}, null);
}

export async function deleteAccountRemoteHost(baseUrl: string, hostId: string): Promise<void> {
	await request(baseUrl, "DELETE", `/${encodeURIComponent(hostId)}`, undefined, null);
}
