// The page a loopback sign-in server (cloud-auth.ts, provider-auth-flow.ts)
// shows in the browser once the OAuth redirect lands: a quiet confirmation in
// the browser's light or dark theme, with one small mark, one heading naming
// the service, at most one supporting line, and one next step. When this build
// owns the ao-app:// link, the success page also returns to Orchestrator on
// its own.
//
// It confirms only what has happened by the time it is sent: the provider
// authorized the sign-in. Claude and GitHub credentials are saved afterwards
// (the cloud:connectProviderAuth handler in cloud-auth.ts, and the renderer
// for GitHub), and that save can still fail, so the desktop app reports it,
// not this page.
//
// Used by Claude Code and GitHub sign-in in every build, and by Orchestrator Cloud
// sign-in only when it uses a loopback redirect (development builds, or an
// AO_CLOUD_AUTH_REDIRECT override). Packaged Cloud sign-in returns through a
// hosted HTTPS page instead (resolveCloudAuthRedirect).
//
// Everything is inline: the page is served by a one-shot local server that
// closes right after this response, so it can't ask that server for anything
// else. The logo is bundled at build time and embedded as a data URL; system
// fonts stand in for Geist.

import logoSvg from "../../assets/ao-logo.svg?raw";

export type AuthCallbackProvider = "cloud" | "claude-code" | "github";

/**
 * Brings Orchestrator to the front. No ao-app:// handler acts on this host, so
 * opening it only focuses the app's window (main.ts handleCloudDeepLinkAndFocus).
 */
export const APP_RETURN_URL = "ao-app://open";

/**
 * The return link for a sign-in page, only when this build owns ao-app://.
 * A development build signing in through a loopback redirect gives the scheme
 * to the installed app (registerCloudProtocol), so following the link there
 * would bring up the wrong app.
 */
export function authCallbackReturnUrl(isPackaged: boolean): string | undefined {
	return isPackaged ? APP_RETURN_URL : undefined;
}

export interface AuthCallbackPage {
	provider: AuthCallbackProvider;
	ok: boolean;
	/** GitHub only: the signed-in login, shown on the success page. */
	login?: string;
	/** Where the page sends the browser back to the app, if anywhere. */
	returnUrl?: string;
}

// An <img> rather than inline markup, so the logo's own ids and gradients
// can't clash with the page's.
const LOGO_SRC = `data:image/svg+xml;base64,${Buffer.from(logoSvg).toString("base64")}`;

/** How long the success page shows before it returns to the app. */
const RETURN_DELAY_MS = 1500;

const PROVIDER_NAME: Record<AuthCallbackProvider, string> = {
	cloud: "Orchestrator",
	"claude-code": "Claude Code",
	github: "GitHub",
};

function escapeHtml(value: string): string {
	return value
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
		.replace(/'/g, "&#39;");
}

const STYLES = `
:root {
	color-scheme: light dark;
	--bg: #fafafa;
	--fg: #141414;
	--muted: #5c5c5c;
	--button: #141414;
	--button-fg: #fafafa;
	--ring: #5c5c5c;
}
@media (prefers-color-scheme: dark) {
	:root {
		--bg: oklch(0.153 0.006 257);
		--fg: oklch(0.988 0.003 257);
		--muted: oklch(0.737 0.021 257);
		--button: oklch(0.93 0.007 257);
		--button-fg: oklch(0.228 0.013 257);
		--ring: oklch(0.737 0.021 257);
	}
}
* { box-sizing: border-box; }
html, body { margin: 0; height: 100%; }
body {
	background: var(--bg);
	color: var(--fg);
	font: 15px/1.5 -apple-system, BlinkMacSystemFont, "SF Pro Text", "Geist", ui-sans-serif, system-ui, "Segoe UI", sans-serif;
	-webkit-font-smoothing: antialiased;
}
main {
	min-height: 100%;
	display: flex;
	flex-direction: column;
	align-items: center;
	justify-content: center;
	padding: 32px 16px 10vh;
	text-align: center;
}
.mark { display: block; width: 32px; height: 32px; margin: 0 0 20px; }
h1 { margin: 0; font-size: 22px; line-height: 1.3; font-weight: 600; letter-spacing: -0.01em; text-wrap: balance; }
p { margin: 8px 0 0; max-width: 28rem; color: var(--muted); text-wrap: balance; }
p strong { color: var(--fg); font-weight: 500; }
.return {
	margin-top: 24px;
	padding: 8px 14px;
	border-radius: 8px;
	background: var(--button);
	color: var(--button-fg);
	font-size: 14px;
	font-weight: 500;
	text-decoration: none;
}
.return:hover { opacity: 0.9; }
.return:focus-visible { outline: 2px solid var(--ring); outline-offset: 2px; }
`;

/** The full HTML document for a loopback sign-in callback response. */
export function renderAuthCallbackPage({ provider, ok, login, returnUrl }: AuthCallbackPage): string {
	const name = PROVIDER_NAME[provider];
	const title = ok ? `Signed in to ${name}` : `Couldn’t sign in to ${name}`;
	// The one supporting line: the GitHub account when there is one, the retry
	// on a failure, and, with no return link, what to do with the tab.
	const lines: string[] = [];
	if (ok && login) lines.push(`Authenticated as <strong>${escapeHtml(login)}</strong>.`);
	if (!ok) lines.push("Try signing in again from Orchestrator.");
	if (!returnUrl) lines.push("You can close this tab.");
	const supporting = lines.length ? `<p>${lines.join(" ")}</p>` : "";
	const action = returnUrl
		? `<a class="return" href="${escapeHtml(returnUrl)}">Return to Orchestrator</a>`
		: "";
	// Success returns on its own; a failure waits, so the person reads it.
	// Opened from script rather than a meta refresh, which some browsers won't
	// follow to an app link. The link stays for a browser that blocked it.
	const script = ok && returnUrl
		? `<script>setTimeout(function(){location.href=${JSON.stringify(returnUrl)};},${RETURN_DELAY_MS});</script>`
		: "";
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>${escapeHtml(title)}</title>
<style>${STYLES}</style>
</head>
<body>
<main>
	<img class="mark" src="${LOGO_SRC}" alt="" width="32" height="32">
	<h1>${escapeHtml(title)}</h1>
	${supporting}
	${action}
</main>
${script}
</body>
</html>`;
}
