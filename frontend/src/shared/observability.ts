export type Severity = "P0" | "P1" | "P2" | "P3";

export type FaultOwner = "ours" | "user" | "external" | "environment";

export type SentryLevel = "fatal" | "error" | "warning" | "info";

export type Triage = {
	severity: Severity;
	owner: FaultOwner;
	level: SentryLevel;
	transient: boolean;
	report: boolean;
};

export type ClassifyInput = {
	category?: string;
	code?: string;
	httpStatus?: number;
	kind?: string;
	unhandled?: boolean;
};

const LEVEL: Record<Severity, SentryLevel> = { P0: "fatal", P1: "error", P2: "warning", P3: "info" };

const TRANSIENT = new Set(["offline", "network_error", "timeout"]);

type Base = { severity: Severity; owner: FaultOwner };

const CATEGORY: Record<string, Base> = {
	native_crash: { severity: "P0", owner: "ours" },
	render_crash: { severity: "P0", owner: "ours" },
	daemon_unavailable: { severity: "P1", owner: "environment" },
	http_5xx: { severity: "P1", owner: "ours" },
	agent_runtime: { severity: "P1", owner: "ours" },
	update: { severity: "P1", owner: "ours" },
	integration: { severity: "P2", owner: "external" },
	rate_limited: { severity: "P2", owner: "external" },
	auth: { severity: "P2", owner: "external" },
	conflict: { severity: "P2", owner: "ours" },
	timeout: { severity: "P2", owner: "environment" },
	network_error: { severity: "P2", owner: "environment" },
	version_skew: { severity: "P2", owner: "ours" },
	push: { severity: "P3", owner: "environment" },
	http_4xx: { severity: "P3", owner: "user" },
	not_found: { severity: "P3", owner: "ours" },
	validation: { severity: "P3", owner: "user" },
	permission: { severity: "P3", owner: "environment" },
	offline: { severity: "P3", owner: "environment" },
};

const CODE: Record<string, Partial<Base>> = {
	AGENT_BINARY_NOT_FOUND: { severity: "P1", owner: "user" },
	RUNTIME_PREREQUISITE_MISSING: { severity: "P1", owner: "user" },
	WORKSPACE_LOCKED: { severity: "P1", owner: "ours" },
	BRANCH_CHECKED_OUT_ELSEWHERE: { severity: "P1", owner: "ours" },
	WORKSPACE_CWD_MISMATCH: { severity: "P1", owner: "ours" },
	AGENT_RESUME_FAILED: { severity: "P1", owner: "ours" },
	NOT_A_GIT_REPO: { severity: "P2", owner: "user" },
	BRANCH_NOT_FETCHED: { severity: "P2", owner: "user" },
	INVALID_BRANCH: { severity: "P2", owner: "user" },
	PROJECT_NOT_RESOLVABLE: { severity: "P2", owner: "user" },
	BAD_PASSWORD: { severity: "P2", owner: "user" },
	LOCKED_OUT: { severity: "P2", owner: "user" },
	SCM_UNAVAILABLE: { severity: "P1", owner: "external" },
	PR_OPERATION_FAILED: { severity: "P2", owner: "external" },
	REVIEW_OPERATION_FAILED: { severity: "P2", owner: "external" },
	INTERNAL_ERROR: { severity: "P1", owner: "ours" },
};

const KIND: Record<string, Base> = {
	internal: { severity: "P1", owner: "ours" },
	conflict: { severity: "P2", owner: "ours" },
	forbidden: { severity: "P2", owner: "external" },
	not_found: { severity: "P3", owner: "ours" },
	invalid: { severity: "P3", owner: "user" },
};

export function classifyError(input: ClassifyInput): Triage {
	const category = input.category?.trim();
	const code = input.code?.trim();

	let base: Base =
		(category && CATEGORY[category]) || (input.kind && KIND[input.kind]) || { severity: "P2", owner: "ours" };

	if (code && CODE[code]) base = { ...base, ...CODE[code] };

	const unavailable = input.httpStatus === 503 && category === "http_5xx" && !!code;

	let severity = base.severity;
	if (input.unhandled && (severity === "P2" || severity === "P3")) severity = "P1";
	if (input.httpStatus && input.httpStatus >= 500 && !unavailable && severity === "P2")
		severity = "P1";
	if (unavailable) severity = "P2";

	const transient = (category ? TRANSIENT.has(category) : false) || unavailable;
	const report = severity === "P0" || severity === "P1" || (severity === "P2" && !transient);

	return { severity, owner: base.owner, level: LEVEL[severity], transient, report };
}
