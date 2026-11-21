import AsyncStorage from "@react-native-async-storage/async-storage";
import { forgetBoard } from "./boardSnapshot";
import { CLOUD_HOST_ID } from "./cloud/mapping";
import { hostMachineIdentity } from "./config";
import { flushReviewPersistence } from "./files/reviewPersistence";
import { forgetHostReviews } from "./files/reviewStore";
import { LAST_ROUTE_KEY } from "./lastRoute";
import { forgetSpawnDraftFor } from "./spawnDraft";

// What the phone keeps so the app survives being closed in the background (a
// machine's last board, the New task draft, unsent review comments, the last
// screen) belongs to a machine or to the account signed in. It goes with them:
// forgetting a machine, or an account signing out or giving way to another,
// leaves nothing of theirs for whoever uses the phone next.

/**
 * Runs every deletion, each tried a second time if it fails (storage that
 * failed once often answers the next time), and only then fails with the
 * first error left: one that fails never stops the others, and the caller
 * learns the phone still holds something it was asked to forget.
 */
async function forgetAll(what: string, deletions: Array<() => Promise<void>>): Promise<void> {
	const results = await Promise.allSettled(deletions.map((deletion) => deletion().catch(() => deletion())));
	const failed = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");
	if (!failed.length) return;
	console.warn(`[forget] ${failed.length} of ${deletions.length} deletions failed for ${what}`, ...failed.map((result) => result.reason));
	throw failed[0].reason;
}

/** Forgets what was kept for a machine that was forgotten. Rejects if any of it is still on the phone. */
export async function forgetMachineState(hostId: string): Promise<void> {
	forgetHostReviews(hostId);
	await forgetAll("a forgotten machine", [
		() => forgetBoard(hostMachineIdentity(hostId)),
		() => forgetSpawnDraftFor({ hostId }),
		() => AsyncStorage.removeItem(LAST_ROUTE_KEY),
		// The reviews' removal reaches the phone before this resolves, not after a delay the app may not live through.
		() => flushReviewPersistence(),
	]);
}

/** Forgets what was kept while an account was signed in, once it signed out or another signed in. Rejects if any of it is still on the phone. */
export async function forgetAccountState(accountId: string): Promise<void> {
	forgetHostReviews(CLOUD_HOST_ID);
	await forgetAll("a signed-out account", [
		() => forgetSpawnDraftFor({ accountId }),
		() => AsyncStorage.removeItem(LAST_ROUTE_KEY),
		() => flushReviewPersistence(),
	]);
}
