// The page a loopback sign-in server (cloud-auth.ts, provider-auth-flow.ts)
// shows in the browser once the OAuth redirect lands: a quiet, centered note in
// the browser's light or dark theme, what just happened as three short
// checks, and, when this build owns the ao-app:// link, an automatic hop back
// to Orchestrator.
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
	/** Where the success page sends the browser back to the app, if anywhere. */
	returnUrl?: string;
}

// An <img> rather than inline markup, so the logo's own ids and gradients
// can't clash with the page's.
const LOGO_SRC = `data:image/svg+xml;base64,${Buffer.from(logoSvg).toString("base64")}`;

/** How long the success page shows before it returns to the app. */
const RETURN_DELAY_MS = 1500;

interface ProviderCopy {
	name: string;
	successTitle: string;
	successSteps: readonly [string, string, string];
}

const COPY: Record<AuthCallbackProvider, ProviderCopy> = {
	cloud: {
		name: "Orchestrator Cloud",
		successTitle: "Signed in to Orchestrator Cloud",
		successSteps: ["Signed in through your browser", "Verified with Orchestrator Cloud", "Handed back to Orchestrator"],
	},
	"claude-code": {
		name: "Claude",
		successTitle: "Claude is connected",
		successSteps: ["Signed in to Claude", "Token received", "Saved in Orchestrator"],
	},
	github: {
		name: "GitHub",
		successTitle: "GitHub is connected",
		successSteps: ["Signed in to GitHub", "Access granted", "Saved in Orchestrator"],
	},
};

function escapeHtml(value: string): string {
	return value
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;")
		.replace(/"/g, "&quot;")
		.replace(/'/g, "&#39;");
}

// Each mark is one 20×20 drawing, ring and glyph together, so the glyph sits
// at the ring's exact centre at any size. The glyphs' bounding boxes are
// centred on (10, 10).
const GLYPH = {
	done: "M6.25 10.25l2.5 2.5 5-5.5",
	bad: "M7.25 7.25l5.5 5.5M12.75 7.25l-5.5 5.5",
	idle: "M7 10h6",
} as const;

function step(kind: keyof typeof GLYPH, text: string): string {
	const mark = `<svg class="mark" viewBox="0 0 20 20" aria-hidden="true"><circle cx="10" cy="10" r="9.25"/><path d="${GLYPH[kind]}"/></svg>`;
	return `<li class="${kind}">${mark}<span>${escapeHtml(text)}</span></li>`;
}

function steps(copy: ProviderCopy, ok: boolean): string {
	if (ok) return copy.successSteps.map((text) => step("done", text)).join("");
	return [
		step("done", "Your browser came back to Orchestrator"),
		step("bad", `${copy.name} sign-in didn’t finish`),
		step("idle", "Nothing was saved"),
	].join("");
}

const STYLES = `
:root {
	color-scheme: light dark;
	--bg: #fafafa;
	--fg: #141414;
	--muted: #5c5c5c;
	--faint: #8a8a8a;
	--line: rgb(0 0 0 / 0.12);
	--ok: #16a34a;
	--bad: #dc2626;
	--button: #141414;
	--button-fg: #fafafa;
	--ease: cubic-bezier(0.23, 1, 0.32, 1);
}
@media (prefers-color-scheme: dark) {
	:root {
		--bg: oklch(0.153 0.006 257);
		--fg: oklch(0.988 0.003 257);
		--muted: oklch(0.737 0.021 257);
		--faint: oklch(0.58 0.031 257);
		--line: rgb(255 255 255 / 0.14);
		--ok: #4ade80;
		--bad: #f87171;
		--button: oklch(0.93 0.007 257);
		--button-fg: oklch(0.228 0.013 257);
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
	animation: rise 500ms var(--ease) both;
}
.logo { display: block; width: 48px; height: 48px; margin: 0 0 20px; }
h1 { margin: 0; font-size: 24px; line-height: 1.25; font-weight: 600; letter-spacing: -0.01em; text-wrap: balance; }
.lede { margin: 12px 0 0; max-width: 30rem; color: var(--muted); text-wrap: balance; }
.lede strong { color: var(--fg); font-weight: 500; }
ul {
	list-style: none;
	margin: 28px 0 0;
	padding: 0;
	display: grid;
	gap: 10px;
	text-align: left;
	font-size: 14px;
}
li { display: flex; align-items: center; gap: 10px; }
li.idle { color: var(--faint); }
.mark {
	flex: none;
	width: 18px;
	height: 18px;
	color: var(--faint);
	fill: none;
	stroke-linecap: round;
	stroke-linejoin: round;
}
.mark circle { fill: color-mix(in srgb, currentColor 12%, transparent); stroke: color-mix(in srgb, currentColor 55%, transparent); stroke-width: 1.5; }
.mark path { stroke: currentColor; stroke-width: 1.75; }
li.done .mark { color: var(--ok); }
li.bad .mark { color: var(--bad); }
.return {
	margin-top: 28px;
	display: inline-flex;
	align-items: center;
	gap: 8px;
	padding: 8px 14px;
	border-radius: 8px;
	background: var(--button);
	color: var(--button-fg);
	font-size: 14px;
	font-weight: 500;
	text-decoration: none;
}
.return:hover { opacity: 0.9; }
.return:focus-visible { outline: 2px solid var(--faint); outline-offset: 2px; }
.note { margin: 14px 0 0; font-size: 13px; color: var(--faint); }
@keyframes rise { from { opacity: 0; transform: translateY(4px); } }
@media (prefers-reduced-motion: reduce) { main { animation: none; } }
`;

/** The full HTML document for a loopback sign-in callback response. */
export function renderAuthCallbackPage({ provider, ok, login, returnUrl }: AuthCallbackPage): string {
	const copy = COPY[provider];
	const title = ok ? copy.successTitle : `Couldn’t sign in to ${copy.name}`;
	const lede = ok
		? login
			? `Authenticated as <strong>${escapeHtml(login)}</strong>.`
			: "You’re all set."
		: "Return to Orchestrator and try signing in again.";
	// Success hops back on its own; a failure waits, so the person reads why.
	const autoReturn = ok && returnUrl !== undefined;
	const action = returnUrl
		? `<a class="return" id="return" href="${escapeHtml(returnUrl)}">Return to Orchestrator</a>`
		: "";
	const note = autoReturn
		? `<p class="note" id="note">Taking you back to Orchestrator…</p>`
		: ok
			? `<p class="note">You can close this window and return to Orchestrator.</p>`
			: "";
	// Opened from script rather than a meta refresh, which some browsers won't
	// follow to an app link. The note changes once the hand-off has been tried,
	// so a browser that blocked or dismissed it still leaves a way back.
	const script = autoReturn
		? `<script>setTimeout(function(){location.href=${JSON.stringify(returnUrl)};setTimeout(function(){var n=document.getElementById("note");if(n)n.textContent="You can close this window.";},1200);},${RETURN_DELAY_MS});</script>`
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
	<img class="logo" src="${LOGO_SRC}" alt="Orchestrator" width="48" height="48">
	<h1>${escapeHtml(title)}</h1>
	<p class="lede">${lede}</p>
	<ul>${steps(copy, ok)}</ul>
	${action}
	${note}
</main>
${script}
</body>
</html>`;
}
