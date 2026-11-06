import {
	SessionCardView,
	SessionsBoardGridView,
	boardKanbanColumnOrder,
	type BoardSessionPresentation,
	type KanbanColumn,
	type ProductUITranslator,
} from "@aoagents/product-ui";
import {
	Archive,
	ArrowUp,
	Bell,
	CalendarClock,
	ChevronDown,
	ChevronRight,
	Coffee,
	Copy,
	EllipsisVertical,
	File,
	FlaskConical,
	Folder,
	FolderOpen,
	LayoutDashboard,
	LogIn,
	PanelLeft,
	PanelRight,
	Play,
	Plus,
	Route,
	ScanSearch,
	Search,
	Settings,
	ShieldCheck,
	Smartphone,
	Undo2,
	WandSparkles,
} from "lucide-react";
import { AnimatePresence, animate as animateElement, motion, useIsPresent, useReducedMotion } from "motion/react";
import { Fragment, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../../i18n";
import { formatTimeCompact, formatTimeTerse } from "../../lib/format-time";
import { getKanbanColumnView, getSessionStatusDotView } from "../../lib/session-presentation";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { AOMascot } from "../AOMascot";
import { PHONE_SCREEN_WIDTH } from "./OnboardingPhone";
import { ProjectBoardEmpty } from "../BoardEmptyStates";
import { humanBubbleClass } from "../chat/ChatTimelineItems";
import { OrchestratorIcon } from "../icons";
import { NavRowHighlight } from "../NavRowHighlight";
import { topbarTitleClass } from "../PageTopbar";
import { ProductExternalLink } from "../ProductExternalLink";
import { sessionsBoardLabels } from "../SessionsBoardAdapters";
import { TargetIcon } from "../TopbarOpenEditorButton";
import { TopbarButton } from "../TopbarButton";

// The right half of each onboarding step: the real desktop window, drawn at
// the app's true size (1320x860 CSS px) and scaled down as one piece so every
// proportion matches the app. Views that render without app state are the
// real components (SessionsBoardGridView, SessionCardView, TopbarButton,
// AgentAvatar, AOMascot, NavRowHighlight, OrchestratorIcon); the stateful
// shell pieces are static copies of the markup the app renders, with the
// class strings taken verbatim from the named source. Only the macOS
// traffic lights and window frame are drawn here, since the OS paints them.
// Everything is illustration: the stage is aria-hidden and inert.

const EASE_OUT = [0.23, 1, 0.32, 1] as const; // --ease-out
const DURATION_NORMAL = 0.15; // --duration-normal

/** The app as it lays out in a 1040x760 window: the same UI at a smaller
 * window size, so the whole window fits the stage at a readable scale. */
export const APP_WIDTH = 1040;
export const APP_HEIGHT = 760;
/** --size-sidebar-default; a new install has not resized the sidebar. */
const SIDEBAR_WIDTH = 240;
/** Window inset from the stage's left edge. */
const STAGE_INSET = 40;


/** The right half: the app window, scaled as one piece, inset from the left
 * and vertically centred, bleeding past the right edge like a crop of the
 * real app. */
/** The iPhone body's width per height, close to iPhone's 393×852 display
 * once the band and bezel are taken off. */
const PHONE_WIDTH_PER_HEIGHT = 0.475;
const PHONE_GAP = 32;
/** Turned on its vertical axis, its face angled away, then squared up as it lands. */
const PHONE_TURN = 40;
const SLIDE_SPRING = { type: "spring", visualDuration: 0.4, bounce: 0.12 } as const;
/** Rows and cards settling into place: quick, with the faintest overshoot. */
const ROW_SPRING = { type: "spring", visualDuration: 0.5, bounce: 0 } as const;
/** Pages sliding across the center panel. */
const PAGE_FADE = { duration: 0.24, ease: EASE_OUT } as const;

/** A plain iPhone body as a line drawing: one stroke for the body, a second
 * for the display, side buttons as short strokes. No fill, gloss or shadow;
 * only the display is filled, with the page, so the window behind it does not
 * show through. The screen lays out at the phone's logical width
 * (393pt) and is scaled as one piece to fill the display, like the window. */
function PhoneFrame({ height, children }: { height: number; children: ReactNode }) {
	const width = height * PHONE_WIDTH_PER_HEIGHT;
	const radius = width * 0.17;
	const band = Math.max(1.5, width * 0.009);
	const bezel = width * 0.026;
	const displayWidth = width - 2 * (band + bezel);
	const displayHeight = height - 2 * (band + bezel);
	const screenScale = displayWidth / PHONE_SCREEN_WIDTH;
	const button = (side: "left" | "right", top: number, length: number) => (
		<span
			className="absolute w-[3px] rounded-[2px] border border-(--schematic-line)"
			style={{ [side]: -2.5, top: `${top}%`, height: `${length}%` }}
		/>
	);
	return (
		<div className="relative size-full">
			{button("left", 17, 4.5)}
			{button("left", 25, 8)}
			{button("left", 35, 8)}
			{button("right", 27, 12)}
			<div
				className="relative size-full bg-(--schematic-page) shadow-[inset_0_0_0_1px_var(--schematic-line)]"
				style={{ borderRadius: radius, padding: band }}
			>
				<div className="size-full" style={{ borderRadius: radius - band, padding: bezel }}>
					<div className="relative size-full overflow-hidden shadow-[0_0_0_1px_var(--schematic-line)]" style={{ borderRadius: radius - band - bezel }}>
						<div
							className="absolute left-0 top-0 origin-top-left"
							style={{ width: PHONE_SCREEN_WIDTH, height: displayHeight / screenScale, transform: `scale(${screenScale})` }}
						>
							{children}
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}

/** `phone` is the phone's screen; given one, the phone slides in beside the window. */
type AppRect = { left: number; top: number; width: number; height: number };

function sameRect(a: AppRect | null, b: AppRect | null): boolean {
	if (!a || !b) return a === b;
	return Math.abs(a.left - b.left) < 0.5 && Math.abs(a.top - b.top) < 0.5 && Math.abs(a.width - b.width) < 0.5 && Math.abs(a.height - b.height) < 0.5;
}

/** An element's laid-out rect in the window's own (unscaled) coordinates,
 * from the offset chain, so the camera, a sliding page or a card in flight
 * (all transforms) never move what it measures: it is where things land. */
function layoutRect(box: HTMLElement, element: HTMLElement): AppRect | null {
	let left = 0;
	let top = 0;
	let node: HTMLElement | null = element;
	while (node && node !== box) {
		left += node.offsetLeft;
		top += node.offsetTop;
		node = node.offsetParent as HTMLElement | null;
	}
	if (node !== box) return null;
	return { left, top, width: element.offsetWidth, height: element.offsetHeight };
}

const DRAWING_EDGE = "linear-gradient(to right, black 93%, rgb(0 0 0 / 0.6) 96.5%, transparent 100%)";

/** Blur steps for DrawingBlur, softest first. Each comes in along a long ramp
 * overlapping the next, so the blur deepens continuously towards the bottom
 * edge with no band where a layer starts. */
const BLUR_STEPS = [1, 2, 4, 8, 16] as const;
/** The blur only starts this close to the window's bottom edge, so a part of
 * the drawing that merely sits low (the phone) stays sharp; only what runs
 * off the edge goes soft. */
const BLUR_BAND = 96;

/** The drawing goes soft towards the window's bottom edge instead of being
 * cut: blur only, no fade. */
function DrawingBlur() {
	return (
		<div aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0" style={{ height: BLUR_BAND }}>
			{BLUR_STEPS.map((blur, index) => {
				const start = (index / BLUR_STEPS.length) * 70;
				const mask = `linear-gradient(to bottom, transparent ${start}%, black ${start + 30}%)`;
				return (
					<div
						key={blur}
						className="absolute inset-0"
						style={{ backdropFilter: `blur(${blur}px)`, WebkitBackdropFilter: `blur(${blur}px)`, maskImage: mask, WebkitMaskImage: mask }}
					/>
				);
			})}
		</div>
	);
}

/** How far the camera may zoom past the fitted window, so text stays crisp. */
/** How far the camera may zoom past the fitted window: enough to read a menu,
 * not so far that the window stops reading as the app. */
const MAX_ZOOM = 1.35;
/** The camera: a soft spring that eases into a framing and can retarget mid-move. */
const CAMERA_SPRING = { type: "spring", visualDuration: 0.6, bounce: 0.08 } as const;

export function PreviewStage({
	children,
	phone,
	phoneShown = false,
	phoneDelay = 0,
	opaque = false,
	frameWidth,
}: {
	children: ReactNode;
	phone?: ReactNode;
	phoneShown?: boolean;
	/** Seconds to hold the phone (and the window's shift) before it slides. */
	phoneDelay?: number;
	/** Paint the window without transparency (as with the setting off). */
	opaque?: boolean;
	/** Lay the stage out at this width whatever its column's current width, so a
	 * collapsing column crops the stage instead of rescaling it. */
	frameWidth?: number;
}) {
	const ref = useRef<HTMLDivElement>(null);
	const reduceMotion = useReducedMotion();
	const [measured, setMeasured] = useState<{ width: number; height: number } | null>(null);
	const size = measured ? { width: frameWidth ?? measured.width, height: measured.height } : null;
	const hasSize = measured !== null;
	// Where the window's center panel sits (same on every step), so the plate
	// can back it with the panel colour when the window turns opaque; and what
	// the current step asks the camera to frame (its [data-preview-focus]
	// elements, ignoring a step that is on its way out).
	const boxRef = useRef<HTMLDivElement>(null);
	const [panel, setPanel] = useState<AppRect | null>(null);
	const [focus, setFocus] = useState<AppRect | null>(null);
	useLayoutEffect(() => {
		const box = boxRef.current;
		if (!box) return;
		const measure = () => {
			const surface = box.querySelector<HTMLElement>(".center-panel-surface");
			const nextPanel = surface ? layoutRect(box, surface) : null;
			if (nextPanel) setPanel((current) => (sameRect(current, nextPanel) ? current : nextPanel));
			const targets = [...box.querySelectorAll<HTMLElement>("[data-preview-focus]")];
			let union: AppRect | null = null;
			for (const target of targets) {
				const rect = layoutRect(box, target);
				if (!rect) continue;
				if (!union) union = rect;
				else {
					const right = Math.max(union.left + union.width, rect.left + rect.width);
					const bottom = Math.max(union.top + union.height, rect.top + rect.height);
					const left = Math.min(union.left, rect.left);
					const top = Math.min(union.top, rect.top);
					union = { left, top, width: right - left, height: bottom - top };
				}
			}
			setFocus((current) => (sameRect(current, union) ? current : union));
		};
		measure();
		const observer = new MutationObserver(measure);
		observer.observe(box, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-preview-focus"] });
		return () => observer.disconnect();
	}, [hasSize]);
	useLayoutEffect(() => {
		const element = ref.current;
		if (!element) return;
		const measure = () => setMeasured({ width: element.clientWidth, height: element.clientHeight });
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		return () => observer.disconnect();
	}, []);
	// Fit the whole window with an even margin, never above true size.
	const scale = size ? Math.min(1, (size.width - 2 * STAGE_INSET) / APP_WIDTH, (size.height - 2 * STAGE_INSET) / APP_HEIGHT) : 0;
	const left = size ? (size.width - APP_WIDTH * scale) / 2 : 0;
	const top = size ? (size.height - APP_HEIGHT * scale) / 2 : 0;
	// With the phone: the phone takes the window's height at the left inset,
	// and the window slides right to sit a gap after it, cropped by the stage.
	const windowHeight = APP_HEIGHT * scale;
	const phoneWidth = windowHeight * PHONE_WIDTH_PER_HEIGHT;
	const windowShift = phone != null && phoneShown && size ? STAGE_INSET + phoneWidth + PHONE_GAP - left : 0;
	// Off the stage's left edge with room for the tilted corners.
	const phoneOffset = -(STAGE_INSET + phoneWidth * 1.4 + PHONE_GAP);
	// A soft spring: settles with a hint of overshoot, the same both ways, and
	// picks up mid-flight if the step changes again before it lands.
	const transition = reduceMotion ? { duration: 0 } : { ...SLIDE_SPRING, delay: phoneDelay };
	// The camera: the fitted window, or the focus rect filling the stage with an
	// even margin (never smaller than the fit, at most MAX_ZOOM of it), centred.
	let camera = { x: left, y: top, scale };
	if (focus && size && scale) {
		const zoom = Math.max(scale, Math.min(scale * MAX_ZOOM, (size.width - 2 * STAGE_INSET) / focus.width, (size.height - 2 * STAGE_INSET) / focus.height));
		camera = {
			x: size.width / 2 - (focus.left + focus.width / 2) * zoom,
			y: size.height / 2 - (focus.top + focus.height / 2) * zoom,
			scale: zoom,
		};
	}
	return (
		// Not clipped: the drawing runs on past the stage, under the copy and off
		// the window's edges, like a sheet that keeps going.
		<div ref={ref} aria-hidden="true" inert className="pointer-events-none relative min-h-0 flex-1 select-none">
			<div data-opaque={opaque} className="onboarding-preview-stage absolute inset-y-0 left-0" style={{ width: size?.width ?? "100%" }}>
			{size ? (
				// Its own backdrop root (the no-op filter), so the blur below sees only
				// the drawing, never the wallpaper behind it. A short soft edge at the
				// stage's right boundary keeps a zoomed drawing from running under the
				// copy; the rest of the drawing is never faded.
				<motion.div
					className="absolute inset-0"
					style={{ filter: "blur(0px)", maskImage: DRAWING_EDGE, WebkitMaskImage: DRAWING_EDGE }}
					initial={{ x: 0 }}
					animate={{ x: windowShift }}
					transition={transition}
				>
					<motion.div
						ref={boxRef}
						data-preview-box=""
						className="absolute left-0 top-0"
						style={{ width: APP_WIDTH, height: APP_HEIGHT, originX: 0, originY: 0 }}
						initial={false}
						animate={camera}
						transition={reduceMotion ? { duration: 0 } : CAMERA_SPRING}
					>
						{/* The window's glass, tint and shadow: one persistent plate under
						    whichever step's contents are showing. */}
						<div className="onboarding-preview-plate absolute inset-0 rounded-[16px]">
							{panel ? <div className="onboarding-preview-panel-backing absolute" style={panel} /> : null}
						</div>
						{children}
					</motion.div>
					<DrawingBlur />
				</motion.div>
			) : null}
			{size && phone != null ? (
				<motion.div
					className="absolute z-10"
					style={{ left: STAGE_INSET, top, width: phoneWidth, height: windowHeight }}
					// Slides in turned and squares up as it lands; leaves the same way.
					initial={{ x: phoneOffset, rotateY: PHONE_TURN, transformPerspective: 1400 }}
					animate={phoneShown ? { x: 0, rotateY: 0, transformPerspective: 1400 } : { x: phoneOffset, rotateY: PHONE_TURN, transformPerspective: 1400 }}
					transition={transition}
				>
					<PhoneFrame height={windowHeight}>{phone}</PhoneFrame>
				</motion.div>
			) : null}
			</div>
		</div>
	);
}

// ── Window ──────────────────────────────────────────────────────────────────

/** macOS window buttons, the one element the OS draws. Geometry from
 * shared/window-chrome.ts (main.ts passes it to trafficLightPosition): 14px
 * buttons, the red one at x = MAC_WINDOW_BUTTON_X (18) and centred on
 * MAC_TRAFFIC_LIGHT_ROW_HEIGHT / 2 (25), 23px apart. Fills and the bright rim
 * are sampled from the native buttons (display-p3, as the OS paints them). */
const TRAFFIC_LIGHTS = [
	{ background: "linear-gradient(to bottom, color(display-p3 0.906 0.439 0.380) 0%, color(display-p3 0.894 0.388 0.333) 18%, color(display-p3 0.886 0.369 0.314) 36%, color(display-p3 0.882 0.404 0.357) 50%, color(display-p3 0.875 0.431 0.388) 64%, color(display-p3 0.871 0.439 0.404) 79%, color(display-p3 0.871 0.435 0.396) 100%)", rim: "color(display-p3 1.000 0.627 0.580)" },
	{ background: "linear-gradient(to bottom, color(display-p3 0.941 0.718 0.212) 0%, color(display-p3 0.937 0.718 0.051) 18%, color(display-p3 0.945 0.718 0.000) 36%, color(display-p3 0.953 0.761 0.180) 50%, color(display-p3 0.961 0.804 0.306) 64%, color(display-p3 0.965 0.816 0.329) 79%, color(display-p3 0.969 0.816 0.314) 100%)", rim: "color(display-p3 1.000 0.871 0.388)" },
	{ background: "linear-gradient(to bottom, color(display-p3 0.431 0.765 0.259) 0%, color(display-p3 0.373 0.749 0.141) 18%, color(display-p3 0.302 0.725 0.000) 36%, color(display-p3 0.384 0.745 0.180) 50%, color(display-p3 0.431 0.753 0.298) 64%, color(display-p3 0.443 0.753 0.318) 79%, color(display-p3 0.431 0.753 0.294) 100%)", rim: "color(display-p3 0.608 0.925 0.463)" },
];

function TrafficLights() {
	return (
		<div className="absolute left-[18px] top-[18px] z-titlebar flex gap-[9px]">
			{TRAFFIC_LIGHTS.map(({ background, rim }) => (
				<span key={rim} className="size-[14px] rounded-full" style={{ background, boxShadow: `inset 0 0 0 0.75px ${rim}` }} />
			))}
		</div>
	);
}

/** Mirrors TitlebarNav.tsx with the sidebar open: the collapse toggle and the
 * brand, fixed beside the traffic lights. */
function TitlebarCluster() {
	return (
		<div className="absolute top-0 left-titlebar-cluster-left z-titlebar flex h-traffic-light-clearance items-center gap-1">
			<span className="inline-flex">
				<button type="button" tabIndex={-1} className="grid size-control-md place-items-center rounded-md text-passive">
					<PanelLeft className="size-icon-lg" aria-hidden="true" />
				</button>
			</span>
			<div className="flex items-center">
				<div className="overflow-hidden">
					<span className="inline-flex select-none items-center gap-1.5 translate-y-0.5 whitespace-nowrap text-base font-semibold leading-tight tracking-tight-lg text-foreground">
						<span className="inline-flex min-w-0">
							<span className="inline-flex min-w-0 items-center gap-1.5">
								<AOMascot className="h-5.5 w-5.5 shrink-0" />
								<span className="min-w-0 truncate">Orchestrator</span>
							</span>
						</span>
					</span>
				</div>
			</div>
		</div>
	);
}

/** Width of the panel's trailing controls (28px controls, 4px apart), as
 * PageTopbarSurface measures it into --topbar-trailing-width. */
function trailingWidth(count: number) {
	return count * 28 + (count - 1) * 4;
}

/** Mirrors PageTopbarSurface's trailing group (panel-topbar-controls): any
 * page controls, then the notification bell. */
function PanelTrailingControls({ inspector }: { inspector: boolean }) {
	return (
		<div className="workspace-topbar-actions shrink-0 panel-topbar-controls">
			{inspector ? (
				<button type="button" tabIndex={-1} className="topbar-control topbar-control--disabled-affordance items-center topbar-control--icon grid size-control-md place-items-center rounded-md border-0 bg-transparent text-muted-foreground relative isolate">
					<PanelRight className="size-icon-md" aria-hidden="true" />
				</button>
			) : null}
			<span className="inline-flex shrink-0">
				<button type="button" tabIndex={-1} className="topbar-control topbar-control--disabled-affordance items-center topbar-control--icon grid size-control-md place-items-center rounded-md border-0 bg-transparent text-muted-foreground isolate relative">
					<Bell className="size-icon-base" aria-hidden="true" />
				</button>
			</span>
		</div>
	);
}

/** The app window: _shell.tsx's sidebar + CenterPanelShell layout on macOS,
 * with the titlebar cluster, traffic lights and the window frame. */
export function PreviewWindow({
	sidebar,
	children,
	page = "board",
}: {
	sidebar: ReactNode;
	children?: ReactNode;
	/** Session pages add the inspector toggle to the panel's trailing controls;
	 * New chat ("new") is a session page without one. */
	page?: "board" | "new" | "session";
}) {
	const inspector = page === "session";
	return (
		<div
			className="onboarding-preview-window relative overflow-hidden rounded-[16px] text-foreground"
			style={{ width: APP_WIDTH, height: APP_HEIGHT }}
		>
			{/* _shell.tsx: app-shell-root > shell-content-row */}
			<div className="flex h-full min-h-0 flex-col bg-sidebar text-foreground">
				<div className="flex min-h-0 w-full flex-1 overflow-x-hidden">
					{/* ui/sidebar.tsx container below the traffic-light band; its
					    right margin tucks under the panel inset (styles.css). */}
					<div className="relative shrink-0" style={{ width: SIDEBAR_WIDTH, marginRight: "calc(-1 * var(--size-center-panel-inset-mac))" }}>
						<div className="absolute bottom-0 left-0 top-traffic-light-clearance flex w-full border-r border-transparent pl-px">
							<div className="flex h-full w-full flex-col bg-sidebar">{sidebar}</div>
						</div>
					</div>
					<main className="flex min-w-0 flex-1 flex-col overflow-x-hidden">
						<div className="relative min-h-0 flex-1 overflow-x-hidden">
							<div className={cn("center-panel-shell center-panel-shell--mac", page !== "board" && "center-panel-shell--session")}>
								<div
									className="center-panel-surface relative has-topbar-controls"
									style={{ "--topbar-trailing-width": `${trailingWidth(inspector ? 2 : 1)}px` } as CSSProperties}
								>
									<div className="relative flex min-h-0 flex-1 flex-col">{children}</div>
									<PanelTrailingControls inspector={inspector} />
								</div>
							</div>
						</div>
					</main>
				</div>
			</div>
			<TitlebarCluster />
			<TrafficLights />
			{/* Window edge: the schematic's one line weight. */}
			<div className="pointer-events-none absolute inset-0 z-titlebar rounded-[16px] shadow-[inset_0_0_0_1px_var(--schematic-line)]" />
		</div>
	);
}

// ── Sidebar ─────────────────────────────────────────────────────────────────

// Sidebar.tsx / ui/sidebar.tsx class strings as rendered (static states only).
const NAV_ROW_CLASS = "flex w-full items-center overflow-hidden p-2 text-left [&>span:last-child]:truncate [&>svg]:size-icon-base [&>svg]:shrink-0 h-control-form rounded-lg px-2.5 text-sm font-medium text-muted-foreground relative h-8 gap-2 [&_svg]:size-icon-md";
const SECTION_ROW_CLASS = "flex h-8 w-full min-w-0 items-center gap-2 px-2.5 text-sm font-medium text-muted-foreground [&_svg]:size-icon-md [&_svg]:shrink-0 relative rounded-lg pr-1";
const ICON_ACTION_CLASS = "sidebar-icon-action grid size-6 shrink-0 place-items-center rounded-md !bg-transparent text-passive [&_svg]:size-icon-md";
const FOOTER_ROW_CLASS = "gap-2.5 rounded-lg px-2.5 text-sm font-medium text-muted-foreground relative flex h-9 w-full items-center text-left";
const FOOTER_ROW_INNER_CLASS = "relative z-[1] flex min-w-0 flex-1 items-center gap-2.5 [&_svg]:size-icon-md [&_svg]:shrink-0";

function NavRow({ icon, label, trailing }: { icon: ReactNode; label: string; trailing?: string }) {
	return (
		<li className="relative">
			<button type="button" tabIndex={-1} className={NAV_ROW_CLASS}>
				<span className="relative z-[1] inline-flex shrink-0 items-center justify-center">{icon}</span>
				<span className="relative z-[1] min-w-0 flex-1 truncate text-left">{label}</span>
				{trailing ? <span className="relative z-[1] ml-auto shrink-0 font-sans text-caption text-passive">{trailing}</span> : null}
			</button>
		</li>
	);
}

/** A section header row (Projects, Scratchpad): label, open chevron, and "+". */
function SectionHeader({ label, className }: { label: string; className?: string }) {
	return (
		<div className={cn(SECTION_ROW_CLASS, className)}>
			<button type="button" tabIndex={-1} className="relative z-[1] flex min-w-0 flex-1 self-stretch items-center gap-2 text-left">
				<span className="truncate">{label}</span>
				<ChevronRight className="size-3.5! shrink-0 rotate-90" aria-hidden="true" />
			</button>
			<span className="relative z-[1] shrink-0">
				<button type="button" tabIndex={-1} className={ICON_ACTION_CLASS}>
					<Plus aria-hidden="true" />
				</button>
			</span>
		</div>
	);
}

export type PreviewSidebarSession = {
	title: string;
	status: BoardSessionPresentation["status"];
	working?: boolean;
	active?: boolean;
	/** Age of the last message, shown at the row's end. */
	minutesAgo?: number;
};
export type PreviewSidebarProject = {
	name: string;
	/** The board or a chat of this project is open. */
	active?: boolean;
	/** The orchestrator's chat is open. */
	orchestratorActive?: boolean;
	sessions?: PreviewSidebarSession[];
};

/** Mirrors Sidebar.tsx: nav rows, the Projects tree with session rows, the
 * Scratchpad header, and the footer (cloud account, Connect mobile, Settings
 * with Stay Awake). */
export function PreviewSidebar({
	projects = [],
	account,
}: {
	projects?: PreviewSidebarProject[];
	/** The signed-in cloud account; the footer shows Sign in to cloud without one. */
	account?: { name: string; avatarUrl?: string } | null;
}) {
	const { t } = useTranslation();
	const [now] = useState(() => Date.now());
	return (
		<>
			<div className="flex min-h-0 flex-1 flex-col">
				<div className="flex flex-col gap-0 p-0 px-3 pt-2" />
				<div className="flex shrink-0 flex-col gap-0 px-2">
					<div className="relative flex w-full min-w-0 flex-col p-0 pb-0.5">
						<div className="w-full text-sm">
							<ul className="flex w-full min-w-0 flex-col gap-0.5">
								<NavRow icon={<Search strokeWidth={1.75} aria-hidden="true" />} label={t("shell.search")} trailing="⌘ K" />
							</ul>
						</div>
					</div>
					<ul className="flex w-full min-w-0 flex-col mb-3 gap-0.5">
						<NavRow icon={<CalendarClock aria-hidden="true" />} label={t("automations.title")} />
						<NavRow icon={<File aria-hidden="true" />} label={t("artifacts.title")} />
					</ul>
					<div className="flex shrink-0 pb-0.5">
						<SectionHeader label={t("shell.projects")} />
					</div>
				</div>
				<div className="flex min-h-0 flex-1 flex-col overflow-hidden gap-0 px-2">
					<ul className="flex w-full min-w-0 flex-col relative gap-0.5 rounded-lg">
						{projects.map((project) => (
							<li key={project.name} className="relative">
								<PreviewProjectRow project={project} />
								{/* Session rows unfold under the project as workers start, and
								    fold away again when the story starts over. */}
								<ul className="flex min-w-0 flex-col border-sidebar-border mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0">
									<AnimatePresence initial={false}>
										{project.sessions?.map((session, index) => (
											<PreviewSessionRow key={session.title} index={index} now={now} session={session} />
										))}
									</AnimatePresence>
								</ul>
							</li>
						))}
					</ul>
					<div className="mb-2 flex min-h-0 flex-col overflow-hidden">
						<div className="shrink-0">
							<SectionHeader label={t("standalone.workspaceName")} className="mt-1" />
						</div>
					</div>
				</div>
			</div>
			{/* Sidebar.tsx footer. */}
			<div className="flex flex-col p-2 relative mt-auto gap-0 overflow-hidden border-t border-border-strong px-2 !py-2">
				<div className="relative flex w-full min-w-46.5 flex-col gap-0.5">
					<div className="relative h-9 overflow-hidden">
						<SidebarAccountRow account={account ?? null} />
					</div>
					<div className="flex flex-col gap-0.5">
						<button type="button" tabIndex={-1} className={FOOTER_ROW_CLASS}>
							<span className={FOOTER_ROW_INNER_CLASS}>
								<Smartphone aria-hidden="true" />
								<span className="tracking-tight">{t("settings.connectMobile")}</span>
							</span>
						</button>
						<div className="flex w-full items-center gap-1">
							<button type="button" tabIndex={-1} className={cn(FOOTER_ROW_CLASS, "min-w-0 flex-1")}>
								<span className={FOOTER_ROW_INNER_CLASS}>
									<Settings aria-hidden="true" />
									<span className="tracking-tight">{t("shell.settings")}</span>
								</span>
							</button>
							<button type="button" tabIndex={-1} className="inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap border-transparent bg-clip-padding text-sm font-normal select-none relative h-9 w-9 rounded-lg border-0 text-muted-foreground">
								<span className="relative z-[1] grid place-items-center">
									<Coffee className="size-icon-md" aria-hidden="true" />
								</span>
							</button>
						</div>
					</div>
				</div>
			</div>
		</>
	);
}

/** Mirrors Sidebar.tsx's project row: folder, name, and the orchestrator and
 * project-actions buttons at its end. */
function PreviewProjectRow({ project }: { project: PreviewSidebarProject }) {
	// The open project's tree is expanded (FolderOpen) even before it has sessions.
	const expanded = Boolean(project.active || project.sessions?.length);
	return (
		<div className={cn("relative", expanded && "sticky top-0 z-20 bg-sidebar")}>
			<div className="relative">
				<button type="button" tabIndex={-1} className={cn(NAV_ROW_CLASS, "pr-sidebar-project-actions", project.active && "text-foreground")}>
					{project.active ? <NavRowHighlight active /> : null}
					<span className="relative z-[1] inline-flex size-icon-md shrink-0 translate-y-px items-center justify-center">
						<span className="inline-flex size-icon-md items-center justify-center">
							{expanded ? <FolderOpen strokeWidth={1.75} aria-hidden="true" /> : <Folder strokeWidth={1.75} aria-hidden="true" />}
						</span>
					</span>
					<span className="relative z-[1] min-w-0 flex-1 translate-y-px truncate">{project.name}</span>
				</button>
			</div>
			<div className="z-chrome absolute inset-y-0 right-1 flex items-center gap-0.5">
				<span className="inline-flex">
					<button type="button" tabIndex={-1} className={cn(ICON_ACTION_CLASS, project.orchestratorActive && "text-foreground")}>
						<OrchestratorIcon aria-hidden="true" strokeWidth={project.orchestratorActive ? 2.5 : 2} />
					</button>
				</span>
				<button type="button" tabIndex={-1} className={ICON_ACTION_CLASS}>
					<EllipsisVertical aria-hidden="true" />
				</button>
			</div>
		</div>
	);
}

/** Mirrors Sidebar.tsx SessionRow: status dot, name, and the message age.
 * A new row unfolds into the list (its height opening, sliding in from the
 * left); its dot changes colour in place as the session's status changes. */
function PreviewSessionRow({ session, now, index }: { session: PreviewSidebarSession; now: number; index: number }) {
	const reduceMotion = useReducedMotion();
	const dot = getSessionStatusDotView({
		status: session.status,
		activity: session.working ? { state: "active", lastActivityAt: new Date(now).toISOString() } : undefined,
	});
	const at = new Date(now - (session.minutesAgo ?? 0) * 60_000).toISOString();
	return (
		<motion.li
			className="relative overflow-hidden pl-0.5"
			initial={reduceMotion ? false : { height: 0, x: -10, opacity: 0 }}
			animate={{ height: 32, x: 0, opacity: 1 }}
			exit={reduceMotion ? undefined : { height: 0, x: -10, opacity: 0 }}
			transition={{ ...ROW_SPRING, delay: reduceMotion ? 0 : index * 0.08 }}
		>
			<div className={cn("relative flex h-8 w-full items-center rounded-lg", session.active && "text-foreground")}>
				{session.active ? <NavRowHighlight active /> : null}
				<div className="relative z-[1] flex min-w-0 flex-1">
					<button type="button" tabIndex={-1} className="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg py-0 pl-1.5 text-left text-sm pr-[36px]">
						<span className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1.5">
							<span className={cn("size-2 rounded-full transition-colors duration-300", dot.className, dot.breathe && "animate-status-pulse")} />
						</span>
						<span className="flex min-w-0 flex-1 items-center gap-1.5">
							<span className={cn("min-w-0 flex-1 truncate", session.active ? "text-foreground" : "text-muted-foreground")}>{session.title}</span>
						</span>
					</button>
				</div>
				<div className="pointer-events-none absolute inset-y-0 right-0 z-chrome">
					<time className="absolute inset-y-0 right-2 z-[1] flex min-w-0 shrink-0 items-center whitespace-nowrap font-sans text-micro tabular-nums text-passive" dateTime={at}>
						{formatTimeTerse(at, now)}
					</time>
				</div>
			</div>
		</motion.li>
	);
}

/** Mirrors Sidebar.tsx CloudSignInRow / CloudAccountRow: the row sign-in changes. */
export function SidebarAccountRow({ account }: { account: { name: string; avatarUrl?: string } | null }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	// Signing in rolls the row over: Sign in to cloud lifts out as the account
	// rises into its place.
	return (
		<AnimatePresence initial={false}>
			<motion.button
				type="button"
				tabIndex={-1}
				key={account ? "account" : "signin"}
				className={cn(FOOTER_ROW_CLASS, "absolute inset-x-0 top-0")}
				initial={reduceMotion ? false : { y: 36 }}
				animate={{ y: 0 }}
				exit={reduceMotion ? undefined : { y: -36 }}
				transition={ROW_SPRING}
			>
				<span className={FOOTER_ROW_INNER_CLASS}>
					{account ? <PreviewAccountAvatar account={account} /> : <LogIn aria-hidden="true" />}
					<span className="min-w-0 truncate tracking-tight">{account ? account.name : t("shell.signInToAOCloud")}</span>
				</span>
			</motion.button>
		</AnimatePresence>
	);
}

/** Mirrors Sidebar.tsx AccountAvatar: the provider picture, else initials. */
function PreviewAccountAvatar({ account }: { account: { name: string; avatarUrl?: string } }) {
	const [broken, setBroken] = useState(false);
	if (account.avatarUrl && !broken) {
		return <img src={account.avatarUrl} alt="" referrerPolicy="no-referrer" className="size-icon-md shrink-0 rounded-full object-cover" onError={() => setBroken(true)} />;
	}
	const initials = account.name.includes("@")
		? account.name.charAt(0).toUpperCase()
		: account.name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part.charAt(0).toUpperCase()).join("");
	return (
		<span className="grid size-icon-md shrink-0 place-items-center rounded-full bg-[var(--color-bg-tertiary)] text-micro font-medium leading-none text-settings-label">
			{initials}
		</span>
	);
}

// ── Board ───────────────────────────────────────────────────────────────────

export type PreviewCard = {
	id: string;
	title: string;
	provider: string;
	column: KanbanColumn;
	status: BoardSessionPresentation["status"];
	displayStatus?: string;
	minutesAgo: number;
	working?: boolean;
	branch?: string;
	/** The worker's open pull request, shown as the card's PR row. */
	pr?: number;
	/** The file the agent is in, and its change so far, for the detailed card. */
	file?: string;
	diff?: ReadonlyArray<readonly ["+" | "-", string]>;
	added?: number;
	removed?: number;
	/** What the worker is asking you, while it waits on you. */
	question?: string;
};

/**
 * Cards keep their identity across renders (data-flip-id): when one changes
 * lane it glides from where it was to where it now sits, and a new one rises
 * into its slot. Positions come from layoutRect, the window's own unscaled
 * coordinates, so the camera's zoom never skews the motion.
 */
export function useCardFlip(rootRef: RefObject<HTMLElement | null>, signature: string, reduceMotion: boolean) {
	const rects = useRef(new Map<string, AppRect>());
	useLayoutEffect(() => {
		const root = rootRef.current;
		const box = root?.closest<HTMLElement>("[data-preview-box]");
		if (!root || !box) return;
		const next = new Map<string, AppRect>();
		let entering = 0;
		for (const element of root.querySelectorAll<HTMLElement>("[data-flip-id]")) {
			const id = element.dataset.flipId!;
			const rect = layoutRect(box, element);
			if (!rect) continue;
			next.set(id, rect);
			if (reduceMotion) continue;
			const previous = rects.current.get(id);
			if (!previous) {
				const delay = 0.1 * entering++;
				element.style.opacity = "0";
				void animateElement(element, { y: [10, 0], scale: [0.98, 1], opacity: [0, 1] }, { ...ROW_SPRING, delay });
				continue;
			}
			const dx = previous.left - rect.left;
			const dy = previous.top - rect.top;
			if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
			// Above the lanes it crosses while it travels.
			element.style.zIndex = "20";
			void animateElement(element, { x: [dx, 0], y: [dy, 0] }, CARD_SPRING).then(() => {
				element.style.zIndex = "";
			});
		}
		rects.current = next;
	}, [rootRef, signature, reduceMotion]);
}

/** A card crossing the board: a touch slower than a row, settling softly. */
const CARD_SPRING = { type: "spring", visualDuration: 0.8, bounce: 0 } as const;

/** The board page as SessionsBoard.tsx renders it: the page topbar (Board,
 * Task, Orchestrator) over the real SessionsBoardGridView with all four lanes,
 * each card the real SessionCardView with BoardSessionCardAdapter's props.
 * Cards settle in and glide between lanes as the story changes them. */
export function PreviewBoard({
	cards,
	focus = false,
}: {
	cards: PreviewCard[];
	/** Frame the cards with the stage's camera. */
	focus?: boolean;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion() ?? false;
	const [now] = useState(() => Date.now());
	const gridRef = useRef<HTMLDivElement>(null);
	useCardFlip(gridRef, cards.map((card) => `${card.id}:${card.column}:${card.status}`).join("|"), reduceMotion);
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);
	const prLabels = {
		short: t("pr.short"),
		states: { closed: t("pr.state.closed"), draft: t("pr.state.draft"), merged: t("pr.state.merged"), open: t("pr.state.open") },
	};
	const sessions: BoardSessionPresentation[] = cards.map((card) => ({
		id: card.id,
		title: card.title,
		provider: card.provider,
		status: card.status,
		displayStatus: card.displayStatus,
		kanbanColumn: card.column,
		branch: card.branch,
		activity: card.working ? { state: "active", lastActivityAt: new Date(now).toISOString() } : undefined,
		updatedAt: new Date(now - card.minutesAgo * 60_000).toISOString(),
	}));
	const byId = new Map(cards.map((card) => [card.id, card]));
	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground">
			{/* PageTopbar + PageTopbarTitle + ProjectBoardActions (header). */}
			<header className="flex h-toolbar w-full shrink-0 items-center workspace-topbar-container center-panel-titlebar relative z-chrome gap-3 border-b border-border-strong pr-(--gap-topbar-control)">
				<div className="flex min-w-0 flex-1 items-center gap-2.5">
					<span className={cn(topbarTitleClass, "inline-flex min-w-0 items-center gap-1.5")}>
						<span className="max-w-content-max truncate">{t("shell.board")}</span>
					</span>
				</div>
				<div className="workspace-topbar-actions shrink-0">
					<span className="inline-flex">
						<TopbarButton className="topbar-control--labeled" data-priority="primary" tabIndex={-1} variant="action">
							<Plus className="size-icon-md" aria-hidden="true" />
							<span data-compact-label="">{t("newTask.task")}</span>
						</TopbarButton>
					</span>
					<span className="inline-flex">
						<TopbarButton className="topbar-control--labeled" data-priority="secondary" tabIndex={-1} variant="primary">
							<OrchestratorIcon className="size-icon-md" aria-hidden="true" />
							<span data-compact-label="">{t("shell.orchestrator")}</span>
						</TopbarButton>
					</span>
				</div>
			</header>
			<div ref={gridRef} className="onboarding-preview-board min-h-0 flex-1 overflow-hidden">
				{sessions.length === 0 ? (
					// A project with no work yet: SessionsBoard shows ProjectBoardEmpty
					// with ProjectBoardActions' "empty" placement.
					<ProjectBoardEmpty
						actions={
							<div className="mt-5 flex items-center gap-2">
								<span className="inline-flex">
									<TopbarButton tabIndex={-1} variant="primary">
										<OrchestratorIcon className="size-icon-md" aria-hidden="true" />
										<span>{t("shell.orchestrator")}</span>
									</TopbarButton>
								</span>
								<span className="inline-flex">
									<TopbarButton tabIndex={-1} variant="accent">
										<Plus className="size-icon-md" aria-hidden="true" />
										<span>{t("shell.newTask")}</span>
									</TopbarButton>
								</span>
							</div>
						}
					/>
				) : <SessionsBoardGridView
					columns={boardKanbanColumnOrder.map((column) => getKanbanColumnView(column, t))}
					labels={sessionsBoardLabels(t)}
					sessions={sessions}
					renderSessionCard={(session) => {
						const card = byId.get(session.id)!;
						return (
							<div data-flip-id={card.id} data-preview-focus={focus ? "" : undefined} className="relative">
								<div className="rounded-lg">
									<SessionCardView
										artifacts={<Fragment />}
										externalLink={ProductExternalLink}
										interactive
										labels={{
											formatTime: (timestamp) => formatTimeCompact(timestamp, { now }),
											intakeIssue: (id) => t("shell.intakeIssue", { id }),
											pr: prLabels,
											updatedAt: (timestamp) => t("shell.lastMessageAt", { time: formatTimeCompact(timestamp, { now }) }),
										}}
										onOpen={() => undefined}
										prs={card.pr ? [{ number: card.pr, state: "open", url: `https://github.com/your-org/your-project/pull/${card.pr}` }] : undefined}
										overlay={
											// BoardSessionCardAdapter's hover-only archive button.
											<span className="inline-flex">
												<span className="inline-flex size-control-md items-center justify-center rounded-sm text-passive pointer-events-none opacity-0">
													<Archive className="size-icon-sm" aria-hidden="true" />
												</span>
											</span>
										}
										renderAvatar={(provider) => <AgentAvatar provider={provider} />}
										session={session}
										translate={translate}
									/>
								</div>
							</div>
						);
					}}
				/>}
			</div>
		</div>
	);
}

// ── Chat ────────────────────────────────────────────────────────────────────

const OPTION_TRIGGER_CLASS = "group/option-menu-trigger settings-option-trigger max-w-full min-w-0";
const SEND_BUTTON_CLASS = "group/button inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap border bg-clip-padding text-sm font-normal outline-none select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 size-control-md size-7 rounded-full border-transparent bg-primary text-primary-foreground";

/** Mirrors SessionTabsTopbar + the session's single chat tab. */
function SessionTopbar({ title, provider, actions }: { title?: string; provider?: string; actions?: ReactNode }) {
	return (
		<div className="flex h-toolbar w-full shrink-0 overflow-hidden">
			<header className="flex h-toolbar w-full shrink-0 items-stretch bg-sidebar">
				<div className="session-topbar-surface flex min-w-0 flex-1">
					<div
						className={cn("flex min-w-0 shrink items-stretch", title && "session-topbar-titlebar-clearance-mac")}
						style={{ width: "100%", "--ao-sidebar-layout-width": `${SIDEBAR_WIDTH}px`, "--ao-sidebar-collapse-progress": 0 } as CSSProperties}
					>
						{title ? (
							<div className="flex h-full min-w-0 flex-1 items-stretch">
								<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
									<div className="scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto">
										<div className="flex w-max items-stretch">
											<div className="flex shrink-0 self-stretch">
												<span className="group relative inline-flex h-full min-w-shell-tab-min max-w-shell-tab-max shrink-0 self-stretch items-center border-r border-border bg-overlay text-foreground">
													<button type="button" tabIndex={-1} className="inline-flex h-full min-w-0 flex-1 items-center gap-2 px-2 text-left text-control leading-none">
														<span className="pointer-events-none grid size-icon-base shrink-0 place-items-center">
															{provider ? <AgentAvatar provider={provider} decorative className="size-icon-sm!" /> : null}
														</span>
														<span className="truncate">{title}</span>
													</button>
													<span className="pointer-events-none absolute inset-x-0 bottom-0 h-0.5 bg-foreground/80" />
												</span>
											</div>
										</div>
									</div>
								</div>
							</div>
						) : null}
					</div>
					{actions ? <div className="session-topbar-actions ml-auto flex shrink-0 items-center">{actions}</div> : null}
				</div>
			</header>
		</div>
	);
}

/** Mirrors SessionTopbarActions for an orchestrator: Task, Open Kanban, the
 * editor hand-off split button, and Run. */
function OrchestratorTopbarActions() {
	const { t } = useTranslation();
	return (
		<div className="session-topbar-session-chrome workspace-topbar-actions flex shrink-0 items-center">
			<div className="workspace-topbar-actions shrink-0">
				<span className="inline-flex">
					<TopbarButton className="topbar-control--labeled" data-priority="primary" tabIndex={-1} variant="primary">
						<Plus className="size-icon-md" aria-hidden="true" />
						<span data-compact-label="">{t("newTask.task")}</span>
					</TopbarButton>
				</span>
				<TopbarButton className="topbar-control--labeled" data-priority="secondary" tabIndex={-1} variant="action">
					<LayoutDashboard className="size-icon-md" aria-hidden="true" />
					<span data-compact-label="">{t("shell.openKanban")}</span>
				</TopbarButton>
				<div className="relative topbar-control-group isolate inline-flex items-center gap-0 rounded-md bg-transparent">
					<span className="inline-flex">
						<button type="button" tabIndex={-1} className="topbar-control topbar-control--disabled-affordance items-center topbar-control--icon grid size-control-md place-items-center rounded-md border-0 text-muted-foreground bg-transparent">
							<TargetIcon className="size-icon-md" />
						</button>
					</span>
					<span className="inline-flex">
						<button type="button" tabIndex={-1} className="topbar-control topbar-control--disabled-affordance items-center topbar-control--icon grid size-control-md place-items-center rounded-md border-0 text-muted-foreground bg-transparent">
							<ChevronDown className="size-icon-sm" aria-hidden="true" />
						</button>
					</span>
				</div>
				<span className="inline-flex">
					<TopbarButton className="topbar-control--static-press" data-priority="secondary" tabIndex={-1} variant="icon">
						<Play className="size-icon-md" aria-hidden="true" />
					</TopbarButton>
				</span>
			</div>
		</div>
	);
}

/** Mirrors ChatComposer's toolbar: attach, the agent/model trigger, the
 * permission mode, and the send button. */
function ComposerToolbar({
	agent,
	permission,
	contextMeter = false,
	menuOpen = false,
	menu,
}: {
	agent?: { id: string; label: string; detail?: string };
	permission: string;
	/** ContextMeter, once a turn has used some of the context window. */
	contextMeter?: boolean;
	menuOpen?: boolean;
	menu?: ReactNode;
}) {
	return (
		<div className="flex h-7 items-center gap-1.5">
			<div className="flex min-w-0 flex-1 items-center gap-0.5">
				<button type="button" tabIndex={-1} className={cn(OPTION_TRIGGER_CLASS, "size-7 shrink-0 justify-center rounded-full bg-transparent p-0 text-muted-foreground")}>
					<Plus className="size-3.5" aria-hidden="true" />
				</button>
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<div className="flex h-7 min-w-0 flex-1 items-center justify-between gap-2">
						<div className="flex h-7 min-w-0 flex-1 items-center gap-0.5">
							{contextMeter ? (
								<div className="flex shrink-0 items-center gap-2">
									<div className="inline-flex size-7 shrink-0 items-center justify-center rounded-full text-logo-accent">
										<svg aria-hidden="true" className="size-5 -rotate-90" viewBox="0 0 24 24">
											<circle cx="12" cy="12" fill="none" r="9" stroke="currentColor" strokeWidth="3" className="text-border" />
											<circle cx="12" cy="12" fill="none" r="9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" pathLength={100} strokeDasharray="8 100" />
										</svg>
									</div>
								</div>
							) : null}
							{agent ? (
								<span className="relative">
									<button
										type="button"
										tabIndex={-1}
										className={cn(OPTION_TRIGGER_CLASS, "h-7 gap-1.5 bg-transparent rounded-lg px-2 text-[12px]! leading-none text-foreground data-[state=open]:bg-interactive-active")}
										data-state={menuOpen ? "open" : "closed"}
									>
										<AgentAvatar provider={agent.id} decorative className="size-icon-base shrink-0 size-3.5" />
										<span className="min-w-0 max-w-[28ch] truncate">{agent.label}</span>
										{agent.detail ? <span className="shrink-0 text-muted-foreground">{agent.detail}</span> : null}
									</button>
									{menu}
								</span>
							) : null}
						</div>
						<div className="flex h-7 shrink-0 items-center gap-1">
							<button type="button" tabIndex={-1} className={cn(OPTION_TRIGGER_CLASS, "h-7 gap-1 bg-transparent rounded-lg text-xs! leading-none text-muted-foreground px-2")}>
								<ShieldCheck className="size-3.5 shrink-0" aria-hidden="true" />
								<span className="min-w-0 max-w-[16ch] truncate">{permission}</span>
							</button>
						</div>
					</div>
				</div>
			</div>
			<div className="flex h-7 shrink-0 items-center">
				<span className="inline-flex">
					<button className={SEND_BUTTON_CLASS} disabled tabIndex={-1} type="button">
						<ArrowUp className="size-3.5" aria-hidden="true" />
					</button>
				</span>
			</div>
		</div>
	);
}

/** Reveals text word by word over `duration`, after `delay` (seconds). */
function useStreamedText(text: string, delay: number, duration: number, enabled: boolean): string {
	const [shown, setShown] = useState(enabled ? 0 : text.length);
	useEffect(() => {
		if (!enabled) {
			setShown(text.length);
			return;
		}
		setShown(0);
		let frame = 0;
		const start = performance.now() + delay * 1000;
		const tick = (now: number) => {
			const progress = Math.min(1, Math.max(0, (now - start) / (duration * 1000)));
			setShown(Math.round(progress * text.length));
			if (progress < 1) frame = requestAnimationFrame(tick);
		};
		frame = requestAnimationFrame(tick);
		return () => cancelAnimationFrame(frame);
	}, [text, delay, duration, enabled]);
	if (shown >= text.length) return text;
	// Whole words only, as a streaming reply arrives.
	const cut = text.lastIndexOf(" ", shown);
	return cut > 0 ? text.slice(0, cut) : "";
}

/** The person's message in the human bubble, rising into the timeline, then
 * the orchestrator's reply streaming in under it with its turn actions. */
function ChatExchange({ message, reply }: { message: string; reply?: string }) {
	const reduceMotion = useReducedMotion() ?? false;
	const streamed = useStreamedText(reply ?? "", 0.7, 1.1, !reduceMotion);
	const done = streamed.length === (reply ?? "").length;
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			<motion.div
				className="group/message flex origin-bottom-right flex-col items-end gap-1"
				initial={reduceMotion ? false : { y: 24, scale: 0.96, opacity: 0 }}
				animate={{ y: 0, scale: 1, opacity: 1 }}
				transition={ROW_SPRING}
			>
				<div className={humanBubbleClass()}>
					<p className="break-words whitespace-pre-wrap text-pretty">{message}</p>
				</div>
				<div className="mt-1 flex h-7 items-center gap-1" />
			</motion.div>
			{reply ? (
				<div className="group/message relative">
					<div>
						<div className="chat-md text-sm text-foreground">
							<p className="my-2 min-h-[1lh] first:mt-0 last:mb-0">{streamed}</p>
						</div>
					</div>
					<div className="flex min-w-0 flex-col gap-1 mt-1">
						<motion.div
							className="-ml-1.5 flex h-7 items-center gap-0.5"
							initial={false}
							animate={done ? { y: 0, opacity: 1 } : { y: 4, opacity: 0 }}
							transition={ROW_SPRING}
						>
							<span className="shrink-0 font-sans text-sm leading-none tabular-nums text-muted-foreground pl-1.5 pr-0.5">4s</span>
							<div className="size-7 shrink-0">
								<button type="button" tabIndex={-1} className="flex shrink-0 items-center justify-center rounded-md text-muted-foreground size-7 px-0 py-0">
									<Copy className="size-3" aria-hidden="true" />
								</button>
							</div>
							<button type="button" tabIndex={-1} className="flex shrink-0 items-center justify-center rounded-md text-muted-foreground size-7">
								<Undo2 className="size-3" aria-hidden="true" />
							</button>
						</motion.div>
					</div>
				</div>
			) : null}
		</div>
	);
}

/** An orchestrator's chat (ChatWorkspace, docked composer): the user's
 * message in the real human bubble, the reply as chat markdown with its turn
 * actions, and the composer. */
export function PreviewChat({
	title,
	provider,
	message,
	reply,
	focus = false,
	runId = 0,
}: {
	title: string;
	provider?: string;
	message?: string;
	reply?: string;
	/** Frame the conversation with the stage's camera. */
	focus?: boolean;
	/** A new id replays the exchange: the message rises in, then the reply streams. */
	runId?: number;
}) {
	const { t } = useTranslation();
	return (
		<div className="flex h-full min-h-0 flex-col">
			<SessionTopbar title={title} provider={provider} actions={<OrchestratorTopbarActions />} />
			<div className="relative min-h-0 flex-1">
				<section
					className="cursor-chat-surface flex h-full min-h-0 min-w-0 w-full flex-col overflow-hidden [font-size:var(--chat-font-size)]"
					data-session-mode="chat"
					data-session-role="orchestrator"
					style={{ "--chat-font-size": "14px" } as CSSProperties}
				>
					<div className="relative flex min-h-0 flex-1 flex-col">
						<div className="flex min-h-0 flex-1 flex-col" data-composer-placement="dock">
							<div className="relative min-h-0 flex-1">
								<div className="chat-scroll-viewport cursor-chat-timeline h-full min-w-0 overflow-hidden px-4 pt-5 pb-0">
									<div className="mx-auto flex min-h-[260px] w-full min-w-0 max-w-3xl flex-col gap-4.5" data-preview-focus={focus && message ? "" : undefined}>
										{message ? <ChatExchange key={runId} message={message} reply={reply} /> : null}
									</div>
								</div>
							</div>
							<div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
								<div className="chat-composer-fade" />
								<div className="mx-auto flex w-full max-w-3xl flex-col gap-2">
									<div className="relative z-10">
										<div className="relative mx-auto flex w-full max-w-3xl flex-col">
											<div className="cursor-chat-composer relative flex flex-col gap-1.5 px-3 pt-3 pb-3">
												<div className="relative max-h-40">
													<div className="py-1 pl-[7px] text-base! leading-relaxed text-muted-foreground min-h-[4.5rem] whitespace-pre-wrap break-words">
														<span>{t("sideChat.messageTheAgent")}</span>
													</div>
												</div>
												<ComposerToolbar agent={provider ? composerAgent(provider) : undefined} contextMeter={Boolean(reply)} permission="Auto" />
											</div>
										</div>
									</div>
								</div>
							</div>
						</div>
					</div>
				</section>
			</div>
		</div>
	);
}

/** What the composer's agent trigger reads: the default model and effort
 * where known (Claude Code's, as the app shows it), else the agent's name. */
function composerAgent(provider: string, label?: string): { id: string; label: string; detail?: string } {
	if (provider === "claude-code") return { id: provider, label: "Opus 5.5", detail: "High" };
	const names: Record<string, string> = { codex: "Codex", opencode: "opencode", aider: "Aider", gemini: "Gemini", cursor: "Cursor" };
	return { id: provider, label: label ?? names[provider] ?? provider };
}

const MENU_ROW_CLASS = "relative isolate flex min-w-0 items-center rounded-none px-2 py-1.5 whitespace-nowrap before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-md data-highlighted:before:bg-interactive-hover data-highlighted:text-foreground text-xs text-foreground";
const MENU_SURFACE_CLASS = "z-overlay settings-menu-surface rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu shadow-(--shadow-popover) chat-settings-menu text-[12px]!";

/** Mirrors AgentSwitchPicker open on its Agent submenu (option-menu surface,
 * search field, Agent / Model / Effort rows, the agent list). */
/** How the app's dropdowns open (data-[state=open] zoom-in-95 from the
 * trigger side): a short scale-up with a fade, from the trigger's corner. */
const MENU_OPEN = { opacity: 1, scale: 1, y: 0 };
const MENU_CLOSED = { opacity: 0, scale: 0.95, y: 4 };

function AgentPickerMenus({ agents, selectedId }: { agents: Array<{ id: string; label: string; dimmed?: boolean }>; selectedId?: string }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion() ?? false;
	// While closing, the menus stop asking the camera to frame them.
	const present = useIsPresent();
	const focus = present ? "" : undefined;
	const selected = agents.find((agent) => agent.id === selectedId);
	const defaults = selected ? composerAgent(selected.id, selected.label) : undefined;
	const menuTransition = (delay: number) => (reduceMotion ? { duration: 0 } : { duration: 0.18, ease: EASE_OUT, delay });
	return (
		<>
			<motion.div
				data-preview-focus={focus}
				className={cn(MENU_SURFACE_CLASS, "absolute bottom-[calc(100%+6px)] left-0 min-w-[14rem] w-64")}
				style={{ transformOrigin: "bottom left" }}
				initial={reduceMotion ? false : MENU_CLOSED}
				animate={MENU_OPEN}
				exit={reduceMotion ? undefined : { ...MENU_CLOSED, transition: { duration: DURATION_NORMAL, ease: EASE_OUT } }}
				transition={menuTransition(0)}
			>
				<div className="flex min-h-0 flex-col">
					<div className="relative shrink-0 pb-1 mb-1 border-b border-border">
						<div className="relative">
							<Search className="pointer-events-none absolute left-3 top-1/2 size-icon-sm -translate-y-1/2 text-settings-muted" aria-hidden="true" />
							<input className="menu-search-input h-auto! rounded-[10px] py-[7px]! pl-8! text-xs!" placeholder={t("agentSwitch.search")} readOnly tabIndex={-1} />
						</div>
					</div>
					<div className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden">
						<div className="model-menu-scroll min-h-0 overflow-hidden">
							<div>
								{([
									[t("agentSwitch.agent"), selected?.label ?? ""],
								] as const).map(([label, value]) => (
									<div key={label} className={cn(MENU_ROW_CLASS, "justify-between gap-3")} data-highlighted="">
										<span>{label}</span>
										<span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
											<span className="min-w-0 truncate">{value}</span>
											<ChevronRight className="size-3 shrink-0" aria-hidden="true" />
										</span>
									</div>
								))}
								{([
									[t("agentSwitch.model"), defaults?.detail ? defaults.label : t("newTask.autoModel")],
									[t("agentSwitch.effort"), defaults?.detail ?? t("newTask.autoModel")],
								] as const).map(([label, value]) => (
									<div key={label} className={cn(MENU_ROW_CLASS, "justify-between gap-3")}>
										<span>{label}</span>
										<span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
											<span className="min-w-0 truncate">{value}</span>
											<ChevronRight className="size-3 shrink-0" aria-hidden="true" />
										</span>
									</div>
								))}
							</div>
						</div>
					</div>
				</div>
			</motion.div>
			{/* The Agent submenu, opened beside its row (positions as Radix places
			    them, relative to the trigger), a beat after the menu. */}
			<motion.div
				data-preview-focus={focus}
				className={cn(MENU_SURFACE_CLASS, "absolute left-[257px] top-[-99px] h-fit! overflow-hidden! min-w-48")}
				style={{ transformOrigin: "top left" }}
				initial={reduceMotion ? false : { ...MENU_CLOSED, y: 0, x: -4 }}
				animate={{ ...MENU_OPEN, x: 0 }}
				exit={reduceMotion ? undefined : { ...MENU_CLOSED, y: 0, x: -4, transition: { duration: DURATION_NORMAL, ease: EASE_OUT } }}
				transition={menuTransition(0.08)}
			>
				<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-hidden">
					{/* An agent that becomes ready unfolds into the list where it belongs. */}
					<AnimatePresence initial={false}>
						{agents.map((agent) => (
							<motion.div
								key={agent.id}
								className="overflow-hidden"
								initial={reduceMotion ? false : { height: 0, x: -6, opacity: 0 }}
								animate={{ height: "auto", x: 0, opacity: 1 }}
								exit={reduceMotion ? undefined : { height: 0, x: -6, opacity: 0 }}
								transition={ROW_SPRING}
							>
								<div
									className={cn(MENU_ROW_CLASS, "gap-2.5 transition-opacity duration-300", agent.dimmed && "opacity-50")}
									data-highlighted={agent.id === selectedId ? "" : undefined}
								>
									<AgentAvatar provider={agent.id} decorative className="size-icon-base shrink-0 mr-2 size-3.5" />
									<span className="min-w-0 truncate">{agent.label}</span>
								</div>
							</motion.div>
						))}
					</AnimatePresence>
				</div>
				<div className={cn(MENU_ROW_CLASS, "mt-1 border-t border-border pt-1")}>{t("agentSelector.manage")}</div>
			</motion.div>
		</>
	);
}

/** The New chat page (ChatWorkspace, centred composer) with the agent picker
 * open: heading, starter chips, composer, and the project chip below it. */
export function PreviewNewChat({
	agents,
	selectedId,
	project,
	menuOpen = false,
}: {
	agents: Array<{ id: string; label: string; dimmed?: boolean }>;
	selectedId?: string;
	project: string;
	/** The agent picker is open (it opens and closes as the app's menus do). */
	menuOpen?: boolean;
}) {
	const { t } = useTranslation();
	const selected = agents.find((agent) => agent.id === selectedId) ?? agents[0];
	const chips: Array<[ReactNode, string]> = [
		[<ScanSearch key="review" className="size-icon-sm shrink-0" aria-hidden="true" />, t("chat.welcome.chip.review")],
		[<FlaskConical key="tests" className="size-icon-sm shrink-0" aria-hidden="true" />, t("chat.welcome.chip.tests")],
		[<Route key="trace" className="size-icon-sm shrink-0" aria-hidden="true" />, t("chat.welcome.chip.trace")],
		[<WandSparkles key="simplify" className="size-icon-sm shrink-0" aria-hidden="true" />, t("chat.welcome.chip.simplify")],
	];
	return (
		<div className="flex h-full min-h-0 flex-col">
			<SessionTopbar />
			<section
				className="cursor-chat-surface flex h-full min-h-0 min-w-0 w-full flex-col overflow-hidden [font-size:var(--chat-font-size)]"
				data-session-mode="chat"
				data-session-role="worker"
				style={{ "--chat-font-size": "14px" } as CSSProperties}
			>
				<div className="relative flex min-h-0 flex-1 flex-col">
					<div className="flex min-h-0 flex-1 flex-col justify-center pb-inspector-tabs" data-composer-placement="center">
						<div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
							<div className="chat-composer-fade" />
							<div className="mx-auto flex w-full max-w-3xl flex-col gap-2 data-[empty]:max-w-2xl" data-empty="true">
								<h1 className="mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl">{t("chat.welcome.heading")}</h1>
								<div className="-mb-2 overflow-hidden">
									<ul className="flex flex-wrap justify-center gap-2 pb-4">
										{chips.map(([icon, label]) => (
											<li key={label}>
												<button type="button" tabIndex={-1} className="welcome-prompt-suggestion inline-flex h-8 items-center gap-1.5 rounded-full border border-border px-3 text-control text-muted-foreground">
													{icon}
													<span className="whitespace-nowrap">{label}</span>
												</button>
											</li>
										))}
									</ul>
								</div>
								<div className="relative z-10" data-composer-tray="">
									<div className="relative mx-auto flex w-full max-w-3xl flex-col">
										<div className="cursor-chat-composer relative flex flex-col gap-1.5 px-3 pt-3 pb-3">
											<div className="relative">
												<div className="min-h-[4.5rem] py-1 pl-[7px] text-base! leading-relaxed text-muted-foreground">
													<span>{t("firstRun.exampleComposerDraft")}</span>
												</div>
											</div>
											<ComposerToolbar
												agent={selected ? composerAgent(selected.id, selected.label) : undefined}
												menu={
													<AnimatePresence>
														{menuOpen && agents.length > 0 ? <AgentPickerMenus key="picker" agents={agents} selectedId={selected?.id} /> : null}
													</AnimatePresence>
												}
												menuOpen={menuOpen && agents.length > 0}
												permission="Auto-approve"
											/>
										</div>
									</div>
								</div>
								<div className="chat-composer-tray flex items-center">
									<button type="button" tabIndex={-1} className="group/settings-option-trigger settings-option-trigger max-w-full min-w-0 h-7 gap-1 bg-transparent rounded-lg px-3 text-xs! leading-none text-muted-foreground w-fit shrink-0">
										<span className="inline-flex min-w-0 items-center gap-1.5">
											<Folder className="shrink-0 size-3.5 text-muted-foreground" aria-hidden="true" />
											<span className="min-w-0 truncate">{project}</span>
										</span>
									</button>
								</div>
							</div>
						</div>
					</div>
				</div>
			</section>
		</div>
	);
}

// ── The scene: one window whose parts animate between steps ────────────────

export type ScenePage = "chat" | "board" | "new";
const PAGES: readonly ScenePage[] = ["chat", "board", "new"];

/**
 * One app window for the whole onboarding: the chrome and sidebar stay put,
 * and the center panel's pages (the orchestrator's chat, the board, New chat)
 * are all mounted side by side, sliding across as the page changes, the way
 * the app moves between them. Only the two pages involved travel; the third
 * waits off to the side. Each page's own parts animate their changes.
 */
export function PreviewScene({
	page,
	sidebar,
	chat,
	board,
	newChat,
}: {
	page: ScenePage;
	sidebar: ReactNode;
	chat: ReactNode;
	board: ReactNode;
	newChat: ReactNode;
}) {
	const reduceMotion = useReducedMotion();
	const previous = useRef(page);
	const leaving = previous.current;
	useEffect(() => {
		previous.current = page;
	}, [page]);
	const content: Record<ScenePage, ReactNode> = { chat, board, new: newChat };
	return (
		<PreviewWindow page={page === "chat" ? "session" : page} sidebar={sidebar}>
			<div className="absolute inset-0 overflow-hidden">
				{PAGES.map((candidate) => {
					const shown = candidate === page;
					const travels = shown || candidate === leaving;
					// The app swaps its center panel in place; here the swap is a short
					// blurred cross-fade so it reads as one change, not a cut.
					return (
						<motion.div
							key={candidate}
							data-scene-page={candidate}
							aria-hidden={!shown}
							className={cn("absolute inset-0 flex flex-col", !shown && "pointer-events-none")}
							initial={false}
							animate={shown ? { opacity: 1, filter: "blur(0px)" } : { opacity: 0, filter: "blur(4px)" }}
							transition={reduceMotion || !travels ? { duration: 0 } : PAGE_FADE}
						>
							{content[candidate]}
						</motion.div>
					);
				})}
			</div>
		</PreviewWindow>
	);
}
