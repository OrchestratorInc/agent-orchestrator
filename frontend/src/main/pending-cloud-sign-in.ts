import type { CloudAccount, CloudPendingSignIn, CloudSignInContext } from "../shared/cloud-account";

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
