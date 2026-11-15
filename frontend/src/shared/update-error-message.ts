export type UpdateErrorPhase = "check" | "download" | "install" | "return";

export type UpdateErrorKind =
	| "timeout"
	| "unavailable"
	| "rate-limited"
	| "not-found"
	| "http-error"
	| "unreachable"
	| "unreadable-feed"
	| "invalid-signature"
	| "detail"
	| "generic";

export interface UpdateErrorInfo {
	kind: UpdateErrorKind;
	phase: UpdateErrorPhase;
	httpStatus?: number;
	detail?: string;
}

export interface UpdateFailure {
	message: string;
	info?: UpdateErrorInfo;
}

const LEAD: Record<UpdateErrorPhase, string> = {
	check: "Couldn't check for updates",
	download: "Download failed",
	install: "Couldn't install the update",
	return: "Couldn't return to your update channel",
};

const NOT_FOUND: Record<UpdateErrorPhase, string> = {
	check: "the update information was not found on the server.",
	download: "the update file was not found on the server.",
	install: "the update file was not found on the server.",
	return: "the update information was not found on the server.",
};

const TRY_SOON = "Try again in a few minutes.";

const REASON: Record<Exclude<UpdateErrorKind, "not-found" | "http-error" | "detail" | "generic">, string> = {
	timeout: `the update server timed out. ${TRY_SOON}`,
	unavailable: `the update server isn't available right now. ${TRY_SOON}`,
	"rate-limited": "the update server is limiting requests. Try again later.",
	unreachable: "the update server couldn't be reached. Check your connection and try again.",
	"unreadable-feed": `the update information from the server couldn't be read. ${TRY_SOON}`,
	"invalid-signature":
		"the update's signature couldn't be verified, so it was not installed. Download the latest build manually and install it over this one.",
};

const TIMED_OUT_PATTERN = /\b(?:request timed out|ETIMEDOUT|ESOCKETTIMEDOUT)\b/i;
const UNREACHABLE_PATTERN =
	/\bnet::|\b(?:ENOTFOUND|EAI_AGAIN|ECONNREFUSED|ECONNRESET|ENETUNREACH|EHOSTUNREACH)\b|\bsocket hang up\b|\bcould not connect to the server\b|\bInternet connection appears to be offline\b|\bhostname could not be found\b|\bnetwork connection was lost\b/i;

const MAX_FIRST_LINE_CHARS = 300;

const MAX_DISPLAYABLE_CHARS = 1000;

const RAW_DUMP_PATTERNS = [
	/<\/?(?:!doctype|html|head|body|title|div|p)\b/i,
	/\bHeaders: /,
	/set-cookie/i,
	/\n\s+at /,
	/\.asar\b/i,
	/\bHttpError\b/,
	/\bERR_UPDATER_/,
];

const UNREADABLE_FEED_CODES = new Set(["ERR_UPDATER_INVALID_RELEASE_FEED", "ERR_UPDATER_INVALID_UPDATE_INFO"]);
const MISSING_RELEASE_CODES = new Set(["ERR_UPDATER_LATEST_VERSION_NOT_FOUND", "ERR_UPDATER_NO_PUBLISHED_VERSIONS"]);

export function looksLikeRawUpdateError(text: string): boolean {
	return text.length > MAX_DISPLAYABLE_CHARS || RAW_DUMP_PATTERNS.some((pattern) => pattern.test(text));
}

export function displayableUpdateError(text: string | undefined, fallback: string): string {
	return text && !looksLikeRawUpdateError(text) ? text : fallback;
}

export function isManifest404Error(err: unknown): boolean {
	if (errorCode(err) === "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND") return true;
	const msg = updateErrorText(err);
	return msg.includes("HttpError: 404") && /\.yml\b/i.test(msg);
}

export function describeUpdateFailure(err: unknown, phase: UpdateErrorPhase): UpdateFailure {
	const info = updateErrorInfo(err, phase);
	return info ? { message: updateErrorSentence(info), info } : { message: updateErrorText(err) };
}

export function userFacingUpdateError(err: unknown, phase: UpdateErrorPhase): string {
	return describeUpdateFailure(err, phase).message;
}

export function updateErrorSentence(info: UpdateErrorInfo): string {
	const lead = LEAD[info.phase];
	switch (info.kind) {
		case "generic":
			return `${lead}. ${TRY_SOON}`;
		case "detail":
			return `${lead} — ${info.detail ?? ""}`;
		case "not-found":
			return `${lead} — ${NOT_FOUND[info.phase]}`;
		case "http-error":
			return `${lead} — the update server returned an error (HTTP ${info.httpStatus ?? ""}). Try again later.`;
		default:
			return `${lead} — ${REASON[info.kind]}`;
	}
}

function updateErrorInfo(err: unknown, phase: UpdateErrorPhase): UpdateErrorInfo | undefined {
	const raw = updateErrorText(err);
	const as = (kind: UpdateErrorKind): UpdateErrorInfo => ({ kind, phase });
	if (raw === "") return as("generic");
	if (isManifest404Error(err)) return as("not-found");
	const status = httpStatus(err, raw);
	if (status === 408 || status === 504) return as("timeout");
	if (status !== undefined && status >= 500) return as("unavailable");
	if (status === 429 || (status === 403 && /rate limit/i.test(raw))) return as("rate-limited");
	if (status === 404) return as("not-found");
	if (status !== undefined) return { kind: "http-error", phase, httpStatus: status };
	if (TIMED_OUT_PATTERN.test(raw)) return as("timeout");
	if (UNREACHABLE_PATTERN.test(raw)) return as("unreachable");
	const code = errorCode(err);
	if (code === "ERR_UPDATER_INVALID_SIGNATURE" || /\bis not signed by the application owner\b/.test(raw)) {
		return as("invalid-signature");
	}
	if (UNREADABLE_FEED_CODES.has(code) || /^Cannot parse (?:releases feed|update info)\b/.test(raw)) {
		return as("unreadable-feed");
	}
	if (MISSING_RELEASE_CODES.has(code) || /^(?:Unable to find latest version|No published versions)\b/.test(raw)) {
		return as("not-found");
	}
	if (!looksLikeRawUpdateError(raw)) return undefined;
	const firstLine = raw.split("\n", 1)[0].replace(/^Error: /, "").trim().replace(/[:,]$/, "");
	return firstLine && firstLine.length <= MAX_FIRST_LINE_CHARS && !looksLikeRawUpdateError(firstLine)
		? { kind: "detail", phase, detail: firstLine }
		: as("generic");
}

export function updateErrorText(err: unknown): string {
	if (err instanceof Error) return err.message;
	return err == null ? "" : String(err);
}

function errorCode(err: unknown): string {
	const code = (err as { code?: unknown } | null | undefined)?.code;
	return typeof code === "string" ? code : "";
}

function httpStatus(err: unknown, raw: string): number | undefined {
	const statusCode = (err as { statusCode?: unknown } | null | undefined)?.statusCode;
	if (typeof statusCode === "number" && statusCode >= 400 && statusCode <= 599) return statusCode;
	const match = /\bHttpError: (\d{3})\b/.exec(raw);
	return match ? Number(match[1]) : undefined;
}
