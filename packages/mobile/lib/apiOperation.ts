// Names a daemon request by its route, never by what it was about. Pure, so the
// privacy rule is unit-tested: a path segment survives only when it is one of
// the daemon's fixed route words below. Everything else (a session, project or
// PR id, a review id, a file name) becomes `:id`, and a file path after a
// file-serving segment collapses to `*`. A word missing from this list
// degrades to `:id`; it can never leak. `apiOperation.test.ts` reads `api.ts`
// and fails when a route the phone calls uses a word that is not listed.

const ROUTE_WORDS = new Set([
	"api", "v1",
	"accounts", "agents", "approvals", "artifact-files", "artifact-versions", "artifacts", "attachments", "auto-inject-ci",
	"auto-inject-review", "auto-review", "blob", "branches", "browser", "button", "cancel", "chat",
	"chat-view", "cleanup", "clone", "close", "codex", "comments", "config-options",
	"conversation", "decision", "delegate", "device", "devices", "diffs", "dirs", "endpoints",
	"file", "files", "fork", "fork-options", "fs", "github-availability", "github-owners",
	"history", "identity", "imports", "initialize", "inputs", "interface-transition",
	"interrupt", "kill", "live", "manifest", "merge", "messages", "models", "notice-acknowledgement",
	"notifications", "open", "orchestrators", "pairings", "pin", "pr", "prepare",
	"prepare-git", "prewarm", "preview", "projects", "provider-switch", "prs", "push",
	"quality", "read-all", "refresh", "released", "remote-host", "account-token",
	"renders", "rerequest", "resolve", "restore", "resume-agent", "reviews", "revision",
	"screenshot", "search", "selection", "send", "sessions", "settings", "shell-terminals", "skills", "steer-or-send",
	"switch", "sync", "telemetry", "tree", "trigger", "validate", "workspace",
]);

// After these, the rest of the path is a file the user's workspace holds, even
// when a file happens to share a name with a route word. (`renders` is not one:
// a render id is followed by route words such as `artifact`.)
const FILE_PARENTS = new Set(["files", "artifact-files"]);

const METHODS = new Set(["GET", "POST", "PUT", "PATCH", "DELETE"]);

/** "POST /api/v1/sessions/:id/send", the same shape as the desktop's operation. */
export function apiOperation(method: string | undefined, path: string): string {
	const verb = (method ?? "GET").toUpperCase();
	const segments = path.split(/[?#]/, 1)[0].split("/");
	const out: string[] = [];
	for (let i = 0; i < segments.length; i += 1) {
		const segment = segments[i];
		if (segment === "") {
			out.push("");
			continue;
		}
		if (ROUTE_WORDS.has(segment)) {
			out.push(segment);
			// A file-serving route's tail is a workspace path; keep only that it had one.
			if (FILE_PARENTS.has(segment) && segments.slice(i + 1).some((rest) => rest !== "")) {
				out.push("*");
				break;
			}
			continue;
		}
		out.push(":id");
	}
	return `${METHODS.has(verb) ? verb : "OTHER"} ${out.join("/").slice(0, 160)}`;
}

const OPERATION = /^(GET|POST|PUT|PATCH|DELETE|OTHER) (\/.{0,160})$/;

/**
 * Whether `operation` has apiOperation()'s shape: every path segment a route
 * word or `:id`, with `*` only last. The send-time check, so a caller that
 * skipped apiOperation() cannot put a name into a Sentry event.
 */
export function isApiOperation(operation: unknown): operation is string {
	if (typeof operation !== "string") return false;
	const match = OPERATION.exec(operation);
	if (!match) return false;
	const segments = match[2].split("/").slice(1);
	return segments.every((segment, index) =>
		ROUTE_WORDS.has(segment) || segment === ":id" || (segment === "*" && index === segments.length - 1));
}

/** The route words, exported for the drift test only. */
export const API_ROUTE_WORDS: ReadonlySet<string> = ROUTE_WORDS;
