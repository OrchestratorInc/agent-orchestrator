/**
 * The text a person sees when an update operation fails (#5755).
 *
 * electron-updater reports a failed request as one free-text Error, and that
 * message can carry the whole HTTP exchange: the response's HTML body, every
 * header (set-cookie included) and, because the GitHub provider wraps the inner
 * failure's `stack`, frames with local app.asar paths. Settings used to render
 * that verbatim. The main process now describes failures with
 * describeUpdateFailure and logs the original. The status carries both an
 * English sentence and the UpdateErrorInfo behind it, so the renderer can show
 * the sentence in the person's language. The renderer runs any text it shows
 * through displayableUpdateError, so an error shape nobody has met yet still
 * cannot fill the screen.
 */

/**
 * The operation that failed. It decides how the sentence starts. "return" is
 * the check that leaves a pinned PR build for the home channel.
 */
export type UpdateErrorPhase = "check" | "download" | "install" | "return";

/**
 * Why an update operation failed, as far as the person needs to know. "detail"
 * keeps a short first line of an unrecognized failure; "generic" says nothing
 * more than which operation failed.
 */
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

/** A rewritten failure, in a form the renderer can localize. */
export interface UpdateErrorInfo {
	kind: UpdateErrorKind;
	phase: UpdateErrorPhase;
	/** The HTTP status, for "http-error". */
	httpStatus?: number;
	/** The failure's own first line, for "detail". Shown untranslated. */
	detail?: string;
}

/** The English sentence for a failure, plus what it says when it was rewritten. */
export interface UpdateFailure {
	message: string;
	/** Absent when the message is AO's own sentence, passed through as-is. */
	info?: UpdateErrorInfo;
}

const LEAD: Record<UpdateErrorPhase, string> = {
	check: "Couldn't check for updates",
	download: "Download failed",
	install: "Couldn't install the update",
	return: "Couldn't return to your update channel",
};

// One wording for a missing manifest, a 404 and a feed with no release, so one
// failure never reads two ways.
const NOT_FOUND: Record<UpdateErrorPhase, string> = {
	check: "the update information was not found on the server.",
	download: "the update file was not found on the server.",
	install: "the update file was not found on the server.",
	return: "the update information was not found on the server.",
};

const TRY_SOON = "Try again in a few minutes.";

// The renderer's settings.updates.error.* keys hold the same English, so a
// localized sentence reads the same as this one in English.
const REASON: Record<Exclude<UpdateErrorKind, "not-found" | "http-error" | "detail" | "generic">, string> = {
	timeout: `the update server timed out. ${TRY_SOON}`,
	unavailable: `the update server isn't available right now. ${TRY_SOON}`,
	"rate-limited": "the update server is limiting requests. Try again later.",
	unreachable: "the update server couldn't be reached. Check your connection and try again.",
	"unreadable-feed": `the update information from the server couldn't be read. ${TRY_SOON}`,
	// Windows refuses a build its publisher check rejects, and the message appends
	// the whole Authenticode result as JSON. Retrying fetches the same build.
	"invalid-signature":
		"the update's signature couldn't be verified, so it was not installed. Download the latest build manually and install it over this one.",
};

// Failures below HTTP, bare or wrapped inside a provider's message: a socket
// timeout from builder-util-runtime, Chromium's net:: codes, Node's socket
// errors and the NSURLError sentences Squirrel reports on macOS ("The request
// timed out.", "Could not connect to the server."). AO's own "Update check
// timed out" deadline message is not matched and passes through.
const TIMED_OUT_PATTERN = /\b(?:request timed out|ETIMEDOUT|ESOCKETTIMEDOUT)\b/i;
const UNREACHABLE_PATTERN =
	/\bnet::|\b(?:ENOTFOUND|EAI_AGAIN|ECONNREFUSED|ECONNRESET|ENETUNREACH|EHOSTUNREACH)\b|\bsocket hang up\b|\bcould not connect to the server\b|\bInternet connection appears to be offline\b|\bhostname could not be found\b|\bnetwork connection was lost\b/i;

// A dump's first line is often the cause ("Something odd" above a stack). It is
// shown when it is short and clean, so a failure that retrying will not fix
// still says what went wrong.
const MAX_FIRST_LINE_CHARS = 300;

/**
 * Longest updater message shown as-is: the bound the restart dialog used to
 * truncate to. AO's own longest sentence, the non-writable install location
 * with its path, stays well under it.
 */
const MAX_DISPLAYABLE_CHARS = 1000;

// Fragments that only appear in a dump: an HTML response body, the header block
// builder-util-runtime appends to an HttpError, a stack frame, a path inside the
// packaged app, or electron-updater's own class and error-code names.
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

/** True when the text is an updater dump rather than a sentence for a person. */
export function looksLikeRawUpdateError(text: string): boolean {
	return text.length > MAX_DISPLAYABLE_CHARS || RAW_DUMP_PATTERNS.some((pattern) => pattern.test(text));
}

/** The text to render for an update failure, or `fallback` when it is a dump. */
export function displayableUpdateError(text: string | undefined, fallback: string): string {
	return text && !looksLikeRawUpdateError(text) ? text : fallback;
}

// isManifest404Error checks whether the error is a 404 on a release
// manifest YAML file — a routine condition that should not be surfaced
// to users as an error dialog.
export function isManifest404Error(err: unknown): boolean {
	if (errorCode(err) === "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND") return true;
	const msg = updateErrorText(err);
	return msg.includes("HttpError: 404") && /\.yml\b/i.test(msg);
}

/**
 * One short sentence for a failed update operation, and what it says.
 *
 * Known failures are explained; AO's own messages pass through; anything else
 * that looks like a dump keeps a clean first line or becomes a generic line.
 * Chromium `net::` failures get the connection sentence too: the status line
 * swaps it for localized restart guidance when the status carries netError
 * (#3526), but the check-error notice, a download and the restart dialog
 * render the text itself.
 */
export function describeUpdateFailure(err: unknown, phase: UpdateErrorPhase): UpdateFailure {
	const info = updateErrorInfo(err, phase);
	return info ? { message: updateErrorSentence(info), info } : { message: updateErrorText(err) };
}

/** The English sentence alone; see describeUpdateFailure. */
export function userFacingUpdateError(err: unknown, phase: UpdateErrorPhase): string {
	return describeUpdateFailure(err, phase).message;
}

/** The English sentence for a rewritten failure. */
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

// undefined means the text is AO's own sentence and is shown as-is.
function updateErrorInfo(err: unknown, phase: UpdateErrorPhase): UpdateErrorInfo | undefined {
	const raw = updateErrorText(err);
	const as = (kind: UpdateErrorKind): UpdateErrorInfo => ({ kind, phase });
	if (raw === "") return as("generic");
	if (isManifest404Error(err)) return as("not-found");
	// Checked before the error codes: the GitHub provider wraps an HTTP failure
	// on /releases/latest in "Cannot parse releases feed", so the outer code
	// says "bad feed" while the status inside says what actually happened.
	const status = httpStatus(err, raw);
	if (status === 408 || status === 504) return as("timeout");
	if (status !== undefined && status >= 500) return as("unavailable");
	if (status === 429 || (status === 403 && /rate limit/i.test(raw))) return as("rate-limited");
	if (status === 404) return as("not-found");
	if (status !== undefined) return { kind: "http-error", phase, httpStatus: status };
	// Also checked before the codes: a timeout on /releases/latest arrives under
	// the same "Cannot parse releases feed" code as an unreadable feed.
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

/** An update error's text: its message, or "" when there is none. */
export function updateErrorText(err: unknown): string {
	if (err instanceof Error) return err.message;
	return err == null ? "" : String(err);
}

function errorCode(err: unknown): string {
	const code = (err as { code?: unknown } | null | undefined)?.code;
	return typeof code === "string" ? code : "";
}

// builder-util-runtime's HttpError carries statusCode, but a provider that wraps
// it keeps only the text, where the status follows the class name.
function httpStatus(err: unknown, raw: string): number | undefined {
	const statusCode = (err as { statusCode?: unknown } | null | undefined)?.statusCode;
	if (typeof statusCode === "number" && statusCode >= 400 && statusCode <= 599) return statusCode;
	const match = /\bHttpError: (\d{3})\b/.exec(raw);
	return match ? Number(match[1]) : undefined;
}
