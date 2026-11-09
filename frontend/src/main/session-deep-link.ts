import { app } from "electron";
import { parseArtifactLink, parseSessionLink, sessionLink } from "../renderer/lib/session-links";

// ao://sessions/{project}/{session} opens a session from outside the app. The
// URL shape is the in-app contract in renderer/lib/session-links.ts; this module
// only decides whether an OS-delivered string may reach the renderer at all.
// Links navigate and nothing else: they are never run, spawned, or handed to a
// shell, and only the session route is allowed through.

const SCHEME_PREFIX = /^ao:\/\//i;
const MAX_LINK_LENGTH = 2048;
// Whitespace and control characters, raw or after percent-decoding.
const UNSAFE_CHARACTERS = /[\s\u0000-\u001f\u007f]/;

export type SessionDeepLinkEvent =
	| { url: string }
	| { error: "malformed" | "unsupported" };

/** The first argv entry that claims the ao:// scheme. */
export function findSessionDeepLinkArg(argv: readonly string[]): string | undefined {
	return argv.find((value) => SCHEME_PREFIX.test(value));
}

/**
 * Turn an OS-delivered ao:// string into the canonical session URL, or say why
 * it cannot be opened: `malformed` for anything that is not a well-formed AO
 * link (credentials, ports, other authorities, bad escapes, query or fragment),
 * `unsupported` for a well-formed link to a route this app does not open.
 */
export function classifySessionDeepLink(raw: string): SessionDeepLinkEvent {
	if (raw.length > MAX_LINK_LENGTH || UNSAFE_CHARACTERS.test(raw) || !SCHEME_PREFIX.test(raw)) return { error: "malformed" };
	// Some launchers (Windows browsers) append a slash to a bare URL.
	const rest = raw.replace(SCHEME_PREFIX, "").replace(/\/$/, "");
	const slash = rest.indexOf("/");
	const authority = (slash < 0 ? rest : rest.slice(0, slash)).toLowerCase();
	if (/[@:?#%]/.test(authority) || rest.includes("?") || rest.includes("#")) return { error: "malformed" };
	if (authority !== "sessions") return authority ? { error: "unsupported" } : { error: "malformed" };
	const normalized = `ao://sessions${slash < 0 ? "" : rest.slice(slash)}`;
	const target = parseSessionLink(normalized);
	if (target) {
		if (UNSAFE_CHARACTERS.test(target.projectId) || UNSAFE_CHARACTERS.test(target.sessionId)) return { error: "malformed" };
		return { url: sessionLink(target.projectId, target.sessionId) };
	}
	return { error: parseArtifactLink(normalized) ? "unsupported" : "malformed" };
}

export type SessionDeepLinkRouter = {
	/** Focus the window and deliver the link, or queue it until the renderer asks. */
	handle(raw: string): void;
	/** The queued link, once; the renderer calls this after it starts listening. */
	take(): SessionDeepLinkEvent | null;
};

export function createSessionDeepLinkRouter(options: {
	focus: () => void;
	/** Send to a renderer that is listening; false when it is not ready yet. */
	deliver: (event: SessionDeepLinkEvent) => boolean;
}): SessionDeepLinkRouter {
	let pending: SessionDeepLinkEvent | null = null;
	return {
		handle(raw) {
			options.focus();
			const event = classifySessionDeepLink(raw);
			if (options.deliver(event)) pending = null;
			else pending = event;
		},
		take() {
			const event = pending;
			pending = null;
			return event;
		},
	};
}

/**
 * Claim ao:// for the packaged app only. A development build must never take
 * the scheme from the installed app (macOS binds it to the stock Electron.app,
 * Windows to a worktree script), so unpackaged builds leave it alone.
 */
export function registerSessionProtocol(isPackaged: boolean = app.isPackaged): boolean {
	if (!isPackaged) return false;
	app.setAsDefaultProtocolClient("ao");
	return true;
}
