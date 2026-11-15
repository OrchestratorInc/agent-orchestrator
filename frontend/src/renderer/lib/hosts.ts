export type HostId = string;

export const LOCAL_HOST: HostId = "local";

export const HOST_UNREACHABLE_CODES: ReadonlySet<string | undefined> = new Set(["UPSTREAM_UNAVAILABLE", "HOST_IDENTITY_UNVERIFIED"]);

export type Ref = {
	host: HostId;
	id: string;
};

export function isLocal(host: HostId): boolean {
	return host === LOCAL_HOST;
}

export function refKey(ref: Ref): string {
	return `${encodeURIComponent(ref.host)}:${encodeURIComponent(ref.id)}`;
}

export function sessionUiKey(id: string, host?: HostId): string {
	return host && !isLocal(host) ? `remote:${refKey({ host, id })}` : id;
}

export function parseRefKey(key: string): Ref {
	const separator = key.indexOf(":");
	if (separator === -1) throw new Error(`malformed ref key: ${key}`);
	return {
		host: decodeURIComponent(key.slice(0, separator)),
		id: decodeURIComponent(key.slice(separator + 1)),
	};
}
