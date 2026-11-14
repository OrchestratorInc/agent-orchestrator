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
		avatarUrl?: string;
	};
	organizations?: CloudOrganization[];
	storedAt: string;
}

export interface CloudSignInContext {
	webDistinctId?: string;
}

export interface CloudPendingSignIn {
	account: CloudAccount;
	signIn: CloudSignInContext;
}

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

export function isWebDistinctId(value: unknown): value is string {
	return typeof value === "string" && WEB_DISTINCT_ID_PATTERN.test(value) && !NOT_A_VISITOR_ID.has(value.toLowerCase());
}
