import { cloudCpFetch } from "./cloud-cp/renderer-client";

export type AccountRemoteHost = { hostId: string; label: string; url: string; token: string };

/** AO Cloud refused a host sync, with the HTTP status it answered. */
export class AccountHostSyncError extends Error {
	constructor(message: string, readonly status: number) {
		super(message);
	}
}

// timeoutMs null waits as long as AO Cloud takes, as fetch does.
async function request(baseUrl: string, method: string, path: string, body?: unknown, timeoutMs: number | null = 10_000): Promise<Response> {
	const response = await cloudCpFetch(`${baseUrl.replace(/\/+$/, "")}/api/cloud/v1/me/hosts${path}`, {
		method,
		headers: { Authorization: "Bearer delegated-to-main-process", ...(body ? { "Content-Type": "application/json" } : {}) },
		body: body ? JSON.stringify(body) : undefined,
		cache: "no-store",
		signal: timeoutMs === null ? undefined : AbortSignal.timeout(timeoutMs),
	});
	if (!response.ok) throw new AccountHostSyncError(`AO Cloud host sync failed (${response.status}).`, response.status);
	return response;
}

/** The account's machines, whether this AO Cloud gives machines fixed addresses, and whether it takes their host keys. */
export type AccountHostDirectory = { hosts: AccountRemoteHost[]; fixedAddresses: boolean; hostKeys: boolean };

export async function readAccountHostDirectory(baseUrl: string): Promise<AccountHostDirectory> {
	const body = await (await request(baseUrl, "GET", "")).json() as Partial<AccountHostDirectory>;
	return { hosts: body.hosts ?? [], fixedAddresses: body.fixedAddresses === true, hostKeys: body.hostKeys === true };
}

export async function listAccountRemoteHosts(baseUrl: string): Promise<AccountRemoteHost[]> {
	return (await readAccountHostDirectory(baseUrl)).hosts;
}

/** A host saved without a url is listed at its fixed address: AO Cloud knows it from the host id. */
export async function saveAccountRemoteHost(baseUrl: string, host: Omit<AccountRemoteHost, "url"> & { url?: string; hostKey?: string }): Promise<void> {
	// AO Cloud may first make the machine's tunnel and DNS record, or delete a
	// standing tunnel a new token retires, with or without a url; either can
	// take more than a minute. No limit: the main process finishes the request
	// anyway, so giving up would report a save that went through as failed.
	await request(baseUrl, "PUT", `/${encodeURIComponent(host.hostId)}`, {
		label: host.label, url: host.url, token: host.token, hostKey: host.hostKey || undefined,
	}, null);
}

export async function deleteAccountRemoteHost(baseUrl: string, hostId: string): Promise<void> {
	// The removal deletes the machine's tunnel first, after any write to it
	// still running, which can take as long as a save without a url.
	await request(baseUrl, "DELETE", `/${encodeURIComponent(hostId)}`, undefined, null);
}
