export type CloudSignInResult =
	| { status: "local" }
	| { status: "authenticated" }
	| { status: "cancelled" }
	| { status: "failed"; message: string };

export type CloudSignInInput = {
	localAuth: boolean;
	openLocalAuth: () => void;
	signInWithWorkOS: () => Promise<boolean>;
};

/** Choose the development credential form or the production hosted flow. */
export async function beginCloudSignIn(input: CloudSignInInput): Promise<CloudSignInResult> {
	if (input.localAuth) {
		input.openLocalAuth();
		return { status: "local" };
	}
	try {
		return await input.signInWithWorkOS()
			? { status: "authenticated" }
			: { status: "cancelled" };
	} catch (cause) {
		return {
			status: "failed",
			message: cause instanceof Error ? cause.message : "That didn't work.",
		};
	}
}
