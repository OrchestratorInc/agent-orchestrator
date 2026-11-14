import type { CloudAccount, CloudPendingSignIn, CloudSignInContext } from "../shared/cloud-account";

/**
 * Holds a completed sign-in's context (CloudSignInContext) until the renderer
 * takes it, once. A sign-in link that starts the app arrives before any window
 * listens (macOS sends open-url before ready, and elsewhere the window may
 * still be loading), so the context cannot ride on the session notification;
 * the renderer takes it on that notification or when it mounts. A newer
 * sign-in replaces it, a sign-out drops it, and a refresh leaves it.
 */
export function createPendingCloudSignIn() {
	let pending: CloudPendingSignIn | undefined;
	return {
		note(account: CloudAccount | null, signIn?: CloudSignInContext): void {
			if (!account) pending = undefined;
			else if (signIn) pending = { account, signIn };
		},
		take(): CloudPendingSignIn | null {
			const taken = pending;
			pending = undefined;
			return taken ?? null;
		},
	};
}
