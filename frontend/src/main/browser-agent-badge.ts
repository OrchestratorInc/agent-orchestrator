// The AO mascot that pops up at the bottom-left of the agent's page when the
// agent starts working in it, then leaves after a few seconds.
//
// The page is a native view above the app's own HTML, so the panel cannot draw
// over it. The badge is its own small transparent native view, stacked above
// the page for those seconds. It is never part of the page: the agent's
// screenshots capture the page alone, and the page's DOM is untouched. It
// runs no script (an inline SVG and CSS animations) and leaves the moment the
// person's pointer reaches it, so it never stands in the way of a click.
import mascotSvg from "../../assets/ao-mascot.svg?raw";

/** The mascot's size, in window pixels. */
export const AGENT_BADGE_LOGO_PX = 72;
/** The badge view: the mascot plus room for its hop and glow. */
export const AGENT_BADGE_VIEW_PX = 108;
/** How long it stays, including its entrance and exit. */
export const AGENT_BADGE_SHOW_MS = 3_000;
/** It pops up when the agent starts again after at least this long quiet,
 *  so a run of commands (with the agent thinking between them) shows it once. */
export const AGENT_BADGE_QUIET_MS = 30_000;

type Rect = { x: number; y: number; width: number; height: number };

export type AgentBadgeView = {
	setBounds: (bounds: Rect) => void;
	setVisible?: (visible: boolean) => void;
	setBackgroundColor?: (color: string) => void;
	webContents: {
		loadURL: (url: string) => unknown;
		on: (event: string, listener: (...args: never[]) => void) => unknown;
		close?: () => void;
	};
};

export function agentBadgeDocument(): string {
	const exitAt = (AGENT_BADGE_SHOW_MS - 400) / 1000;
	return `<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;height:100%;background:transparent;overflow:hidden}
.m{position:absolute;left:14px;bottom:10px;width:${AGENT_BADGE_LOGO_PX}px;height:${AGENT_BADGE_LOGO_PX}px;
transform-origin:50% 90%;filter:drop-shadow(0 6px 12px rgba(96,165,250,.55));
animation:pop .5s cubic-bezier(.34,1.56,.64,1) both,conduct .9s ease-in-out .5s 2,leave .4s ease-in ${exitAt}s forwards}
.m svg{display:block;width:100%;height:100%}
@keyframes pop{from{opacity:0;transform:translateY(28px) scale(.5)}to{opacity:1;transform:none}}
@keyframes conduct{0%,50%,100%{transform:none}25%{transform:translateY(-7px) rotate(-10deg)}75%{transform:translateY(-7px) rotate(10deg)}}
@keyframes leave{to{opacity:0;transform:translateY(14px) scale(.8)}}
@keyframes appear{from{opacity:0}}
@media (prefers-reduced-motion:reduce){.m{animation:appear .2s both,leave .4s ease-in ${exitAt}s forwards}}
</style></head><body><div class="m">${mascotSvg.replace(/^\s*<\?xml[^>]*>\s*/, "")}</div></body></html>`;
}

const AGENT_BADGE_URL = `data:text/html;charset=utf-8,${encodeURIComponent(agentBadgeDocument())}`;

/** Where the badge sits over a page: its bottom-left corner, or nowhere when
 *  the page is too small to spare the corner. */
export function agentBadgeBounds(page: Rect): Rect | undefined {
	if (page.width < AGENT_BADGE_VIEW_PX * 2 || page.height < AGENT_BADGE_VIEW_PX * 2) return undefined;
	return {
		x: Math.round(page.x),
		y: Math.round(page.y + page.height - AGENT_BADGE_VIEW_PX),
		width: AGENT_BADGE_VIEW_PX,
		height: AGENT_BADGE_VIEW_PX,
	};
}

export type AgentBadge = {
	/** Pops the badge up over a page for the session `owner`. */
	show: (owner: unknown, page: Rect) => void;
	/** Moves it with the page it is on, if `owner`'s badge is up. */
	follow: (owner: unknown, page: Rect) => void;
	showing: (owner: unknown) => boolean;
	hide: () => void;
	dispose: () => void;
};

export function createAgentBadge(options: {
	create: () => AgentBadgeView;
	// Stacks the view above every page (re-adding raises it).
	attach: (view: AgentBadgeView) => void;
	detach: (view: AgentBadgeView) => void;
}): AgentBadge {
	let view: AgentBadgeView | undefined;
	let owner: unknown;
	let timer: ReturnType<typeof setTimeout> | undefined;
	const hide = () => {
		if (timer !== undefined) clearTimeout(timer);
		timer = undefined;
		owner = undefined;
		view?.setVisible?.(false);
	};
	const ensure = (): AgentBadgeView => {
		if (view) return view;
		const created = options.create();
		created.setBackgroundColor?.("#00000000");
		// The person's pointer reached it: out of their way at once.
		created.webContents.on("before-mouse-event", () => hide());
		created.webContents.on("will-navigate", ((event: { preventDefault: () => void }) => event.preventDefault()) as never);
		view = created;
		return created;
	};
	return {
		show: (nextOwner, page) => {
			const at = agentBadgeBounds(page);
			if (!at) return;
			const badge = ensure();
			owner = nextOwner;
			// Loading it again restarts the entrance.
			void Promise.resolve(badge.webContents.loadURL(AGENT_BADGE_URL)).catch(() => undefined);
			badge.setBounds(at);
			badge.setVisible?.(true);
			options.attach(badge);
			if (timer !== undefined) clearTimeout(timer);
			timer = setTimeout(hide, AGENT_BADGE_SHOW_MS + 200);
		},
		follow: (current, page) => {
			if (owner === undefined || owner !== current) return;
			const at = agentBadgeBounds(page);
			if (at) view?.setBounds(at);
			else hide();
		},
		showing: (current) => owner !== undefined && owner === current,
		hide,
		dispose: () => {
			hide();
			if (!view) return;
			options.detach(view);
			view.webContents.close?.();
			view = undefined;
		},
	};
}
