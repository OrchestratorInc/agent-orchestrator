import {
	SessionCardView,
	SessionsBoardGridView,
	boardKanbanColumnOrder,
	type BoardPullRequestPresentation,
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
	GitBranch,
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
import { motion, useReducedMotion } from "motion/react";
import { Fragment, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
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
const APP_WIDTH = 1040;
const APP_HEIGHT = 760;
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

/** A plain iPhone body: thin titanium band, black bezel, the display, and the
 * side buttons. No gloss. The screen lays out at the phone's logical width
 * (393pt) and is scaled as one piece to fill the display, like the window. */
function PhoneFrame({ height, children }: { height: number; children: ReactNode }) {
	const width = height * PHONE_WIDTH_PER_HEIGHT;
	const radius = width * 0.17;
	const band = Math.max(1.5, width * 0.009);
	const bezel = width * 0.026;
	const displayWidth = width - 2 * (band + bezel);
	const displayHeight = height - 2 * (band + bezel);
	const screenScale = displayWidth / PHONE_SCREEN_WIDTH;
	const titanium = "linear-gradient(90deg, #2a2a2c, #4d4d50 6%, #2f2f31 50%, #4d4d50 94%, #2a2a2c)";
	const button = (side: "left" | "right", top: number, length: number) => (
		<span
			className="absolute w-[3px] rounded-[2px]"
			style={{ [side]: -2.5, top: `${top}%`, height: `${length}%`, background: "linear-gradient(90deg, #232325, #4a4a4d, #232325)" }}
		/>
	);
	return (
		<div className="relative size-full">
			{button("left", 17, 4.5)}
			{button("left", 25, 8)}
			{button("left", 35, 8)}
			{button("right", 27, 12)}
			<div
				className="relative size-full shadow-[0_0_0_0.5px_rgba(0,0,0,0.9),0_30px_60px_-12px_rgba(0,0,0,0.65)]"
				style={{ borderRadius: radius, padding: band, background: titanium }}
			>
				<div className="size-full bg-black shadow-[inset_0_0_0_0.5px_rgba(255,255,255,0.08)]" style={{ borderRadius: radius - band, padding: bezel }}>
					<div className="relative size-full overflow-hidden" style={{ borderRadius: radius - band - bezel }}>
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

/** An element's rect in the window's own (unscaled) coordinates. */
function appRect(box: HTMLElement, element: HTMLElement): AppRect | null {
	const boxRect = box.getBoundingClientRect();
	const k = boxRect.width / APP_WIDTH;
	if (!k) return null;
	const rect = element.getBoundingClientRect();
	return { left: (rect.left - boxRect.left) / k, top: (rect.top - boxRect.top) / k, width: rect.width / k, height: rect.height / k };
}

/** How far the camera may zoom past the fitted window, so text stays crisp. */
const MAX_ZOOM = 2;
/** The camera: a soft spring that eases into a framing and can retarget mid-move. */
const CAMERA_SPRING = { type: "spring", visualDuration: 0.6, bounce: 0.08 } as const;

export function PreviewStage({
	children,
	phone,
	phoneShown = false,
	opaque = false,
	frameWidth,
}: {
	children: ReactNode;
	phone?: ReactNode;
	phoneShown?: boolean;
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
			const nextPanel = surface ? appRect(box, surface) : null;
			if (nextPanel) setPanel((current) => (sameRect(current, nextPanel) ? current : nextPanel));
			const targets = [...box.querySelectorAll<HTMLElement>("[data-preview-focus]")].filter((element) => !element.closest("[data-stage-exiting]"));
			let union: AppRect | null = null;
			for (const target of targets) {
				const rect = appRect(box, target);
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
		observer.observe(box, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-preview-focus", "data-stage-exiting"] });
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
	const transition = reduceMotion ? { duration: 0 } : SLIDE_SPRING;
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
		<div ref={ref} aria-hidden="true" inert className="pointer-events-none relative min-h-0 flex-1 select-none overflow-hidden border-l border-border">
			<div data-opaque={opaque} className="onboarding-preview-stage absolute inset-y-0 left-0" style={{ width: size?.width ?? "100%" }}>
			{size ? (
				<motion.div className="absolute inset-0" initial={{ x: 0 }} animate={{ x: windowShift }} transition={transition}>
					<motion.div
						ref={boxRef}
						className="absolute left-0 top-0"
						style={{ width: APP_WIDTH, height: APP_HEIGHT, originX: 0, originY: 0 }}
						initial={false}
						animate={camera}
						transition={reduceMotion ? { duration: 0 } : CAMERA_SPRING}
					>
						{/* The window's glass, tint and shadow: one persistent plate under
						    whichever step's contents are showing. */}
						<div className="onboarding-preview-plate absolute inset-0 rounded-[16px] shadow-[0_22px_70px_4px_rgba(0,0,0,0.56),0_0_0_0.5px_#000]">
							{panel ? <div className="onboarding-preview-panel-backing absolute" style={panel} /> : null}
						</div>
						{children}
					</motion.div>
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
			{/* macOS window edge: a 1px light inner ring inside a hairline. */}
			<div className="pointer-events-none absolute inset-0 z-titlebar rounded-[16px] shadow-[inset_0_0_0_1px_rgb(255_255_255/0.12)]" />
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
	dimBody = false,
	account,
	focusAccount = false,
	dimFooter = false,
}: {
	projects?: PreviewSidebarProject[];
	/** Dim everything above the footer. */
	dimBody?: boolean;
	/** The signed-in cloud account; the footer shows Sign in to cloud without one. */
	account?: { name: string; avatarUrl?: string } | null;
	/** Lead with the account row: highlight it and dim the other footer rows. */
	focusAccount?: boolean;
	dimFooter?: boolean;
}) {
	const { t } = useTranslation();
	const [now] = useState(() => Date.now());
	return (
		<>
			<div className={cn("flex min-h-0 flex-1 flex-col", dimBody && "opacity-35")}>
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
							<motion.li
								key={project.name}
								className="relative"
								initial={{ opacity: 0 }}
								animate={{ opacity: 1 }}
								transition={{ duration: DURATION_NORMAL, ease: EASE_OUT }}
							>
								<PreviewProjectRow project={project} />
								{project.sessions?.length ? (
									<ul className="flex min-w-0 flex-col border-sidebar-border py-0.5 mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0 pt-1 pb-px">
										{project.sessions.map((session) => <PreviewSessionRow key={session.title} now={now} session={session} />)}
									</ul>
								) : null}
							</motion.li>
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
					<div className={cn(dimFooter && "opacity-35")}>
						<SidebarAccountRow account={account ?? null} highlight={focusAccount} />
					</div>
					<div className={cn("flex flex-col gap-0.5", (dimFooter || focusAccount) && "opacity-35")}>
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

/** Mirrors Sidebar.tsx SessionRow: status dot, name, and the message age. */
function PreviewSessionRow({ session, now }: { session: PreviewSidebarSession; now: number }) {
	const dot = getSessionStatusDotView({
		status: session.status,
		activity: session.working ? { state: "active", lastActivityAt: new Date(now).toISOString() } : undefined,
	});
	const at = new Date(now - (session.minutesAgo ?? 0) * 60_000).toISOString();
	return (
		<li className="relative pl-0.5">
			<div className={cn("relative flex h-8 w-full items-center rounded-lg", session.active && "text-foreground")}>
				{session.active ? <NavRowHighlight active /> : null}
				<div className="relative z-[1] flex min-w-0 flex-1">
					<button type="button" tabIndex={-1} className="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg py-0 pl-1.5 text-left text-sm pr-[36px]">
						<span className="relative z-[1] inline-flex shrink-0 items-center justify-center px-1.5">
							<span className={cn("size-2 rounded-full", dot.className, dot.breathe && "animate-status-pulse")} />
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
		</li>
	);
}

/** Mirrors Sidebar.tsx CloudSignInRow / CloudAccountRow: the row sign-in changes. */
export function SidebarAccountRow({ account, highlight = false }: { account: { name: string; avatarUrl?: string } | null; highlight?: boolean }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	return (
		<motion.button
			type="button"
			tabIndex={-1}
			key={account ? "account" : "signin"}
			className={cn(FOOTER_ROW_CLASS, highlight && "text-foreground")}
			initial={reduceMotion ? false : { opacity: 0 }}
			animate={{ opacity: 1 }}
			transition={{ duration: DURATION_NORMAL, ease: EASE_OUT }}
		>
			{highlight ? <NavRowHighlight active /> : null}
			<span className={FOOTER_ROW_INNER_CLASS}>
				{account ? <PreviewAccountAvatar account={account} /> : <LogIn aria-hidden="true" />}
				<span className="min-w-0 truncate tracking-tight">{account ? account.name : t("shell.signInToAOCloud")}</span>
			</span>
		</motion.button>
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
	branch?: string;
	provider: string;
	status: BoardSessionPresentation["status"];
	displayStatus?: string;
	minutesAgo: number;
	prs?: Array<Pick<BoardPullRequestPresentation, "number" | "state">>;
	working?: boolean;
};
export type PreviewLane = { column: KanbanColumn; cards: PreviewCard[] };

/** The board page as SessionsBoard.tsx renders it: the page topbar (Board,
 * Task, Orchestrator) over the real SessionsBoardGridView with all four lanes,
 * each card the real SessionCardView with BoardSessionCardAdapter's props. */
export function PreviewBoard({ lanes, stagger = false }: { lanes: PreviewLane[]; stagger?: boolean }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [now] = useState(() => Date.now());
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);
	const prLabels = {
		short: t("pr.short"),
		states: { closed: t("pr.state.closed"), draft: t("pr.state.draft"), merged: t("pr.state.merged"), open: t("pr.state.open") },
	};
	const cards = lanes.flatMap((lane) => lane.cards.map((card) => ({ card, column: lane.column })));
	const order = new Map(cards.map(({ card }, index) => [card.id, index]));
	const sessions: BoardSessionPresentation[] = cards.map(({ card, column }) => ({
		id: card.id,
		title: card.title,
		branch: card.branch,
		provider: card.provider,
		status: card.status,
		displayStatus: card.displayStatus,
		kanbanColumn: column,
		activity: card.working ? { state: "active", lastActivityAt: new Date(now).toISOString() } : undefined,
		updatedAt: new Date(now - card.minutesAgo * 60_000).toISOString(),
	}));
	const byId = new Map(cards.map(({ card }) => [card.id, card]));
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
			<div className="min-h-0 flex-1 overflow-hidden">
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
							<motion.div
								initial={reduceMotion || !stagger ? false : { opacity: 0, y: 4 }}
								animate={{ opacity: 1, y: 0 }}
								transition={{ duration: DURATION_NORMAL, ease: EASE_OUT, delay: stagger ? (order.get(session.id) ?? 0) * 0.12 : 0 }}
							>
								<SessionCardView
									artifacts={<Fragment />}
									branchIcon={<GitBranch aria-hidden="true" className="size-icon-2xs shrink-0" />}
									externalLink={ProductExternalLink}
									interactive
									labels={{
										formatTime: (timestamp) => formatTimeCompact(timestamp, { now }),
										intakeIssue: (id) => t("shell.intakeIssue", { id }),
										pr: prLabels,
										updatedAt: (timestamp) => t("shell.lastMessageAt", { time: formatTimeCompact(timestamp, { now }) }),
									}}
									onOpen={() => undefined}
									overlay={
										// BoardSessionCardAdapter's hover-only archive button.
										<span className="inline-flex">
											<span className="inline-flex size-control-md items-center justify-center rounded-sm text-passive pointer-events-none opacity-0">
												<Archive className="size-icon-sm" aria-hidden="true" />
											</span>
										</span>
									}
									prs={card.prs?.map((pr) => ({ ...pr, url: "" }))}
									renderAvatar={(provider) => <AgentAvatar provider={provider} />}
									session={session}
									translate={translate}
								/>
							</motion.div>
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

/** An orchestrator's chat (ChatWorkspace, docked composer): the user's
 * message in the real human bubble, the reply as chat markdown with its turn
 * actions, and the composer. */
export function PreviewChat({
	title,
	provider,
	message,
	reply,
}: {
	title: string;
	provider?: string;
	message?: string;
	reply?: string;
}) {
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
									<div className="mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-4.5">
										{message ? (
											<div className="flex min-w-0 flex-col gap-2.5">
												<div className="group/message flex flex-col items-end gap-1">
													<div className={humanBubbleClass()}>
														<p className="break-words whitespace-pre-wrap text-pretty">{message}</p>
													</div>
													<div className="mt-1 flex h-7 items-center gap-1" />
												</div>
												{reply ? (
													<div className="group/message relative">
														<div>
															<div className="chat-md text-sm text-foreground">
																<p className="my-2 first:mt-0 last:mb-0">{reply}</p>
															</div>
														</div>
														<div className="flex min-w-0 flex-col gap-1 mt-1">
															<div className="-ml-1.5 flex h-7 items-center gap-0.5">
																<span className="shrink-0 font-sans text-sm leading-none tabular-nums text-muted-foreground pl-1.5 pr-0.5">4s</span>
																<div className="size-7 shrink-0">
																	<button type="button" tabIndex={-1} className="flex shrink-0 items-center justify-center rounded-md text-muted-foreground size-7 px-0 py-0">
																		<Copy className="size-3" aria-hidden="true" />
																	</button>
																</div>
																<button type="button" tabIndex={-1} className="flex shrink-0 items-center justify-center rounded-md text-muted-foreground size-7">
																	<Undo2 className="size-3" aria-hidden="true" />
																</button>
															</div>
														</div>
													</div>
												) : null}
											</div>
										) : null}
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
														<span>Message the agent</span>
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
function AgentPickerMenus({ agents, selectedId }: { agents: Array<{ id: string; label: string; dimmed?: boolean }>; selectedId?: string }) {
	const { t } = useTranslation();
	const selected = agents.find((agent) => agent.id === selectedId);
	const defaults = selected ? composerAgent(selected.id, selected.label) : undefined;
	return (
		<>
			<div className={cn(MENU_SURFACE_CLASS, "absolute bottom-[calc(100%+6px)] left-0 min-w-[14rem] w-64")}>
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
			</div>
			{/* The Agent submenu, opened beside its row (positions as Radix places
			    them, relative to the trigger). */}
			<div className={cn(MENU_SURFACE_CLASS, "absolute left-[257px] top-[-99px] h-fit! overflow-hidden! min-w-48")}>
				<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-hidden">
					{agents.map((agent) => (
						<div
							key={agent.id}
							className={cn(MENU_ROW_CLASS, "gap-2.5", agent.dimmed && "opacity-50")}
							data-highlighted={agent.id === selectedId ? "" : undefined}
						>
							<AgentAvatar provider={agent.id} decorative className="size-icon-base shrink-0 mr-2 size-3.5" />
							<span className="min-w-0 truncate">{agent.label}</span>
						</div>
					))}
				</div>
				<div className={cn(MENU_ROW_CLASS, "mt-1 border-t border-border pt-1")}>{t("agentSelector.manage")}</div>
			</div>
		</>
	);
}

/** The New chat page (ChatWorkspace, centred composer) with the agent picker
 * open: heading, starter chips, composer, and the project chip below it. */
export function PreviewNewChat({
	agents,
	selectedId,
	project,
}: {
	agents: Array<{ id: string; label: string; dimmed?: boolean }>;
	selectedId?: string;
	project: string;
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
													<span>Plan the next step for this feature</span>
												</div>
											</div>
											<ComposerToolbar
												agent={selected ? composerAgent(selected.id, selected.label) : undefined}
												menu={agents.length > 0 ? <AgentPickerMenus agents={agents} selectedId={selected?.id} /> : null}
												menuOpen={agents.length > 0}
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

/** Wraps preview content the step is not about, so the taught part leads. */
export function PreviewDim({ children }: { children: ReactNode }) {
	return <div className="flex h-full min-h-0 flex-1 flex-col opacity-35">{children}</div>;
}
