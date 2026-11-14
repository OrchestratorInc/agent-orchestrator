import { probeRemote, type RemoteHealth } from "./remote-request";
import {
	applyRemoteChanges,
	readRemotes,
	removeRemote,
	updateRemote,
	type RemoteChanges,
	type RemoteEntry,
} from "./remotes-store";

export type RemoteHostView = {
	hostId: string;
	label: string;
	url: string;
};

export function toHostViews(entries: RemoteEntry[]): RemoteHostView[] {
	return entries.map(({ hostId, label, url }) => ({ hostId: hostId ?? "", label, url }));
}

export async function findRemote(path: string, url: string, hostId?: string): Promise<RemoteEntry> {
	const entry = (await readRemotes(path)).find((candidate) => candidate.url === url && (!hostId || candidate.hostId === hostId));
	if (!entry) throw new Error(`no saved host for ${url}`);
	return entry;
}

type Disconnect = (url: string) => Promise<void>;

export async function updateSavedRemote(
	path: string,
	url: string,
	changes: RemoteChanges,
	disconnect: Disconnect,
	probe: (entry: RemoteEntry) => Promise<RemoteHealth> = probeRemote,
): Promise<RemoteHealth> {
	const health = await probe(applyRemoteChanges(await findRemote(path, url), changes));
	if (health !== "online") return health;
	await updateRemote(path, url, changes);
	await disconnect(url);
	return health;
}

export async function removeSavedRemote(path: string, url: string, disconnect: Disconnect): Promise<void> {
	await removeRemote(path, url);
	await disconnect(url);
}
