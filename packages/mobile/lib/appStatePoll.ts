import type { AppStateStatus } from "react-native";

// shouldPoll decides whether the REST poll runs for a given AppState. The poll is
// also the daemon's liveness signal, so stopping it is what makes the desktop's
// live dot go dark when the app is backgrounded — within the daemon's 20s TTL,
// deterministically, instead of whenever the OS happens to suspend the timer.
//
// "inactive" keeps polling on purpose: iOS reports it for the app switcher,
// Control Centre, and permission prompts, none of which mean the user left.
//
// Kept out of store.tsx (which pulls in React Native and can't be imported by
// vitest — see appStatePoll.test.ts) so this predicate stays unit-testable,
// mirroring shouldKeepPolling in connectionError.ts.
//
// The state is widened past AppStateStatus because RN types
// AppState.currentState as `string | null | undefined`.
export function shouldPoll(state: AppStateStatus | string | null | undefined): boolean {
	return state !== "background";
}
