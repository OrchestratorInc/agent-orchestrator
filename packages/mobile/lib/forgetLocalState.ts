import AsyncStorage from "@react-native-async-storage/async-storage";
import { forgetBoard } from "./boardSnapshot";
import { CLOUD_HOST_ID } from "./cloud/mapping";
import { hostMachineIdentity } from "./config";
import { forgetHostReviews } from "./files/reviewStore";
import { LAST_ROUTE_KEY } from "./lastRoute";
import { forgetSpawnDraftFor } from "./spawnDraft";

// What the phone keeps so the app survives being closed in the background (a
// machine's last board, the New task draft, unsent review comments, the last
// screen) belongs to a machine or to the account signed in. It goes with them:
// forgetting a machine, or an account signing out or giving way to another,
// leaves nothing of theirs for whoever uses the phone next.

/** Forgets what was kept for a machine that was forgotten. */
export async function forgetMachineState(hostId: string): Promise<void> {
	forgetHostReviews(hostId);
	await Promise.allSettled([
		forgetBoard(hostMachineIdentity(hostId)),
		forgetSpawnDraftFor({ hostId }),
		AsyncStorage.removeItem(LAST_ROUTE_KEY),
	]);
}

/** Forgets what was kept while an account was signed in, once it signed out or another signed in. */
export async function forgetAccountState(accountId: string): Promise<void> {
	forgetHostReviews(CLOUD_HOST_ID);
	await Promise.allSettled([
		forgetSpawnDraftFor({ accountId }),
		AsyncStorage.removeItem(LAST_ROUTE_KEY),
	]);
}
