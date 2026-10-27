import { clearConfig } from "./config";
import { loadAccount } from "./account";
import { ignoreAccountHost } from "./accountHostPreferences";
import { activeHostMetadata, removeHost, type HostMetadata } from "./hosts";
import { clearOnboardingSkipped } from "./onboardingStore";
import { retryPendingUnpairs, unpairFromServer } from "./push";
import { markMachineUnpaired } from "./pendingUnpair";
import { clearEventCursorsForHost } from "./chat/eventCursor";

/** Forget one saved machine without disturbing the currently selected machine. */
export async function forgetPairedHost(host: HostMetadata, options?: { ignoreAccount?: boolean }): Promise<void> {
	// Do not erase the only credential if durable remote cleanup cannot be saved.
	await unpairFromServer(host);
	if (options?.ignoreAccount !== false) {
		const account = await loadAccount();
		const accountId = account?.id ?? host.accountUserId;
		if (accountId) await ignoreAccountHost(accountId, host.id);
	}

	const cleanup = await Promise.allSettled([
		removeHost(host.id).then(() => markMachineUnpaired(host.id)),
		clearEventCursorsForHost(host),
	]);
	void retryPendingUnpairs().catch(() => {});
	const failed = cleanup.find((result): result is PromiseRejectedResult => result.status === "rejected");
	if (failed) throw failed.reason;
}

// Persist remote cleanup before deleting the selected machine's stored token.
// Network work is retried separately and never holds the UI in a busy state.
// A caller that already chose the machine passes it, so a selection change in
// the meantime (account sync, another screen) cannot redirect the forget.
export async function forgetServer(target?: HostMetadata): Promise<void> {
	// The host record and its token are the actual pairing now; the legacy
	// config is only the last resolved address. Clearing that alone left the
	// machine in the list with its token in the keystore, so the next launch
	// raced its endpoints and silently reconnected to the server just forgotten.
	let host: HostMetadata | null = target ?? null;
	let upstreamError: unknown;
	if (!target) {
		try {
			host = await activeHostMetadata();
		} catch (error) {
			// Metadata is non-secret and normally cannot fail, but a storage failure
			// must not skip the cleanup we can still perform.
			upstreamError = error;
		}
	}
	await unpairFromServer(host);
	if (host) {
		const account = await loadAccount();
		const accountId = account?.id ?? host.accountUserId;
		if (accountId) await ignoreAccountHost(accountId, host.id);
	}

	// Each local deletion is independent. A rejected SecureStore operation must
	// not prevent the config, event cursor, or onboarding flag from being
	// cleared; otherwise "forget" can leave a partially paired phone behind.
	const cleanup = await Promise.allSettled([
		host ? removeHost(host.id).then(() => markMachineUnpaired(host.id)) : Promise.resolve(),
		host ? clearEventCursorsForHost(host) : Promise.resolve(),
		clearConfig(),
		clearOnboardingSkipped(),
	]);
	void retryPendingUnpairs().catch(() => {});
	if (upstreamError) throw upstreamError;
	const failed = cleanup.find((result): result is PromiseRejectedResult => result.status === "rejected");
	if (failed) throw failed.reason;
}
