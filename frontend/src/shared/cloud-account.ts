// The identity provider that minted the active cloud session. "workos" is the
// production AuthKit path; "local" is the dev-only email/password path used to
// sign into a loopback Docker control plane running AO_CLOUD_LOCAL_AUTH (see
// main/cloud-auth-local.ts). Local sessions are gated to unpackaged/dev builds
// against a 127.0.0.1/localhost control plane and never reach production users.
export type CloudAuthProvider = "workos" | "local";

export interface CloudOrganization {
	id: string;
	slug: string;
	displayName: string;
	role: string;
}

export interface CloudAccount {
	authProvider: CloudAuthProvider;
	user: {
		id: string;
		email: string;
		displayName: string;
		/** Profile picture from the identity provider (Google, GitHub), when it has one. */
		avatarUrl?: string;
	};
	/** Organizations the user belongs to. Populated by the local provider; omitted by WorkOS. */
	organizations?: CloudOrganization[];
	storedAt: string;
}

/**
 * What a sign-in that just completed hands the renderer once, next to its
 * account. Not part of the account, and never stored.
 */
export interface CloudSignInContext {
	/**
	 * The visitor's PostHog distinct id from the orchestrator.inc website. Its
	 * sign-in return page adds it only for visitors who accepted analytics there,
	 * so the desktop app can link that website visit to the account.
	 */
	webDistinctId?: string;
}

/**
 * A completed sign-in's context, with the account it signed in, held in main
 * until the renderer takes it (once): on the session notification, or when it
 * mounts if the sign-in link was what started the app.
 */
export interface CloudPendingSignIn {
	account: CloudAccount;
	signIn: CloudSignInContext;
}

// The shape the website's return page itself enforces. The placeholder ids
// PostHog never merges are refused too: a link joins two people for good.
const WEB_DISTINCT_ID_PATTERN = /^[A-Za-z0-9._$:-]{1,200}$/;
const NOT_A_VISITOR_ID = new Set([
	"anonymous",
	"guest",
	"distinctid",
	"distinct_id",
	"id",
	"not_authenticated",
	"email",
	"undefined",
	"true",
	"false",
	"null",
	"0",
	"nan",
	"none",
	"nil",
	"$posthog_cookieless",
]);

/** Whether a value is a website visitor id the desktop app may link. */
export function isWebDistinctId(value: unknown): value is string {
	return typeof value === "string" && WEB_DISTINCT_ID_PATTERN.test(value) && !NOT_A_VISITOR_ID.has(value.toLowerCase());
}
