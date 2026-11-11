import { ArrowUp, Bell, CalendarClock, Check, ChevronDown, Cloud, Code2, Coffee, Cog, EllipsisVertical, File, Folder, GitPullRequest, Layers, LayoutDashboard, ListFilter, LogIn, PanelLeft, PanelRight, Play, Plus, Search, Settings, ShieldCheck, Smartphone, SquarePen } from "@/components/app-icons";
import { AnimatePresence, motion, useReducedMotion, type TargetAndTransition, type Transition } from "motion/react";
import { createContext, useContext, useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { GITHUB_MARK_PATH } from "../GitHubIcon";
import { OrchestratorIcon } from "../icons";
import { AgentOutline } from "./AgentOutline";
import type { PhoneWorker } from "./OnboardingPhone";
import { APP_HEIGHT, APP_WIDTH, type PreviewCard, type ScenePage } from "./OnboardingPreviews";

// The onboarding illustration: the whole app as an isometric line drawing.
// The window lies flat and is projected at fixed 30° axes with no
// perspective, like an engineering drawing. Raised parts (the center panel,
// the composer, cards, the open menu, the phone) are slabs: a top face, the
// two side edges that face the viewer and the line of their base. Every edge
// is one thin stroke; nothing is filled except with the page itself, which
// hides the lines a slab stands in front of. Colour is the page's foreground
// at three strengths and every label is monospace (styles.css
// .onboarding-preview-stage supplies the --schematic-* tokens).

// One smooth curve for everything: a long, gentle ease-out with no bounce.
const EASE_OUT = [0.22, 1, 0.36, 1] as const;
const EASE_IN_OUT = [0.65, 0, 0.35, 1] as const;
const PAGE_FADE = { duration: 0.45, ease: EASE_IN_OUT } as const;
const RISE = { duration: 0.6, ease: EASE_OUT } as const;
const SETTLE = { duration: 0.9, ease: EASE_OUT } as const;

const LINE = "border-(--schematic-line)";
/** Outlines: the heavier line a drawing gives an object's silhouette. */
const OUTLINE = "border-(--schematic-line-strong)";
const TEXT = "text-(--schematic-text)";
const MUTED = "text-(--schematic-text-muted)";
const PASSIVE = "text-(--schematic-text-passive)";
/** Every face is opaque, even in a translucent window, hiding what is behind it. */
const FACE = "bg-(--schematic-solid)";
const ICON = { strokeWidth: 1.25, "aria-hidden": true } as const;

// ── Isometric projection ────────────────────────────────────────────────────

/** Plane x runs up-right and plane y down-right, both at 30° from horizontal. */
const ISO_X = Math.SQRT1_2;
const ISO_Y = Math.SQRT1_2 / Math.sqrt(3);
const ISO = `matrix(${ISO_X}, ${-ISO_Y}, ${ISO_X}, ${ISO_Y}, 0, 0)`;
/** Straight up on screen is (+1, -1) on the plane; per unit it rises this much. */
const LIFT = 2 * ISO_Y;

const WINDOW_WIDTH = 960;
const WINDOW_HEIGHT = 640;
const WINDOW_DEPTH = 14;
const PANEL_DEPTH = 6;
const RAISED_DEPTH = 5;
const MENU_DEPTH = 10;
/** The highest top face above the ground, for fitting. */
const MAX_LIFT = Math.max(WINDOW_DEPTH + PANEL_DEPTH + MENU_DEPTH, 28);
const SIDEBAR_WIDTH = 220;

const PHONE_HEIGHT = 540;
const PHONE_WIDTH = Math.round(PHONE_HEIGHT * 0.475);
const PHONE_GAP = 64;
/** The phone lies left of the window, its bottom end in line with the window's. */
const PHONE_X = -(PHONE_WIDTH + PHONE_GAP);
/** About an iPhone's thickness for its width. */
const PHONE_DEPTH = 28;
const PHONE_SCREEN_WIDTH = 393;
const PHONE_BEZEL = 9;
const PHONE_RADIUS = 42;

const FIT_MARGIN = 20;

type Rect = { x: number; y: number; width: number; height: number };
type Point = [number, number];

/** A point on the plane, `z` above the ground, where the flat drawing shows it. */
function toSheet(x: number, y: number, z = 0): Point {
	return [ISO_X * (x + y), ISO_Y * (y - x) - LIFT * z];
}

type Frame = { left: number; top: number; right: number; bottom: number };
type Camera = { x: number; y: number; scale: number };

function bounds(points: Point[]): Frame {
	const xs = points.map(([x]) => x);
	const ys = points.map(([, y]) => y);
	return { left: Math.min(...xs), top: Math.min(...ys), right: Math.max(...xs), bottom: Math.max(...ys) };
}

/** Everything on the sheet: the window, the phone once it is in, and `extra`. */
function overviewPoints(phone: boolean, extra: Point[]): Point[] {
	const rects: Rect[] = [{ x: 0, y: 0, width: WINDOW_WIDTH, height: WINDOW_HEIGHT }];
	if (phone) rects.push({ x: PHONE_X, y: WINDOW_HEIGHT - PHONE_HEIGHT, width: PHONE_WIDTH, height: PHONE_HEIGHT });
	return [
		...rects.flatMap((r) => [
			toSheet(r.x, r.y, MAX_LIFT),
			toSheet(r.x + r.width, r.y, MAX_LIFT),
			toSheet(r.x, r.y + r.height),
			toSheet(r.x + r.width, r.y + r.height),
		]),
		...extra,
	];
}

/** The camera that shows `frame` centred in the stage's box with `margin`,
 * never closer than `maxScale`. */
function frameCamera(frame: Frame, margin: number, maxScale = Number.POSITIVE_INFINITY): Camera {
	const width = frame.right - frame.left;
	const height = frame.bottom - frame.top;
	const scale = Math.min((APP_WIDTH - 2 * margin) / width, (APP_HEIGHT - 2 * margin) / height, maxScale);
	return {
		scale,
		x: APP_WIDTH / 2 - scale * (frame.left + width / 2),
		y: APP_HEIGHT / 2 - scale * (frame.top + height / 2),
	};
}

// ── Drafting marks ──────────────────────────────────────────────────────────

/** The drawing's numbered callouts: a target dot on the part, a leader out to
 * a balloon, and the part's name beside it. Positions are flat-drawing
 * coordinates (`toSheet`), so the marks stay upright on the sheet. */
type Callout = { label: string; at: Point; balloon: Point };

const BALLOON_R = 9;

function calloutExtent(callout: Callout): Point[] {
	const [bx, by] = callout.balloon;
	const labelWidth = callout.label.length * 6.4 + BALLOON_R + 8;
	return [
		[bx - labelWidth, by - BALLOON_R],
		[bx + BALLOON_R, by + BALLOON_R],
	];
}

function Callouts({ callouts }: { callouts: Callout[] }) {
	return (
		<svg aria-hidden="true" className="absolute left-0 top-0 overflow-visible" width={1} height={1}>
			{callouts.map((callout, index) => {
				const [tx, ty] = callout.at;
				const [bx, by] = callout.balloon;
				const length = Math.hypot(bx - tx, by - ty) || 1;
				const ex = bx - ((bx - tx) / length) * BALLOON_R;
				const ey = by - ((by - ty) / length) * BALLOON_R;
				return (
					<motion.g key={callout.label} initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ ...RISE, delay: 0.35 + index * 0.12 }}>
						<line x1={tx} y1={ty} x2={ex} y2={ey} stroke="var(--schematic-line-strong)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
						<circle cx={tx} cy={ty} r={2} fill="var(--schematic-text)" />
						<circle cx={bx} cy={by} r={BALLOON_R} fill="var(--schematic-solid)" stroke="var(--schematic-line-strong)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
						<text x={bx} y={by + 3.5} textAnchor="middle" fontSize={10} fill="var(--schematic-text)">
							{index + 1}
						</text>
						<text x={bx - BALLOON_R - 6} y={by + 3.5} textAnchor="end" fontSize={11} fill="var(--schematic-text-muted)">
							{callout.label}
						</text>
					</motion.g>
				);
			})}
		</svg>
	);
}

/** A fill colour stacked along the lift: the slab's body. */
function volume(depth: number, fill = "var(--schematic-solid)"): string {
	return Array.from({ length: depth }, (_, step) => `${step + 1}px ${-(step + 1)}px 0 ${fill}`).join(", ");
}

/**
 * A raised plate. The element holds the slab's footprint on whatever it stands
 * on; its children go on the top face, `depth` above. Drawn as the base's two
 * visible edges, the silhouette lines at the corners where the sides turn
 * away, and the outlined top face.
 */
function Slab({
	depth,
	radius,
	outline = false,
	solid = false,
	seams = [],
	className,
	faceClassName,
	style,
	children,
}: {
	depth: number;
	radius: number;
	/** Draw the silhouette with the heavier outline weight. */
	outline?: boolean;
	/** Opaque even in a translucent window, hiding whatever is behind it. */
	solid?: boolean;
	/** Heights up the visible sides where a seam runs round the body. */
	seams?: number[];
	className?: string;
	faceClassName?: string;
	style?: CSSProperties;
	children?: ReactNode;
}) {
	const line = outline ? OUTLINE : LINE;
	const fill = solid ? "bg-(--schematic-solid)" : FACE;
	// Where a rounded corner's outline is tangent to the lift direction.
	const tangent = radius * (1 - Math.SQRT1_2);
	const edge = (position: CSSProperties) => (
		<span
			aria-hidden="true"
			className={cn("absolute h-0 origin-top-left -rotate-45 border-t", line)}
			style={{ ...position, width: depth * Math.SQRT2 }}
		/>
	);
	return (
		<div className={cn("absolute", className)} style={style}>
			<span
				aria-hidden="true"
				className={cn("absolute inset-0", fill)}
				style={{ borderRadius: radius, boxShadow: volume(depth, solid ? "var(--schematic-solid)" : undefined) }}
			/>
			<span aria-hidden="true" className={cn("absolute inset-0 border-b border-l", line)} style={{ borderRadius: radius }} />
			{seams.map((height) => (
				<span
					key={height}
					aria-hidden="true"
					className={cn("absolute inset-0 border-b border-l", LINE)}
					style={{ borderRadius: radius, transform: `translate(${height}px, ${-height}px)` }}
				/>
			))}
			{edge({ left: tangent, top: tangent })}
			{edge({ left: `calc(100% - ${tangent}px)`, top: `calc(100% - ${tangent}px)` })}
			<div
				className={cn("absolute inset-0 border", line, fill, faceClassName)}
				style={{ borderRadius: radius, transform: `translate(${depth}px, ${-depth}px)` }}
			>
				{children}
			</div>
		</div>
	);
}

// ── Status ──────────────────────────────────────────────────────────────────

type Lane = "working" | "action" | "review" | "merge";
const LANES: readonly Lane[] = ["working", "action", "review", "merge"];

function laneOf(status: PreviewCard["status"] | PhoneWorker["status"]): Lane {
	if (status === "needs_input") return "action";
	if (status === "review_pending") return "review";
	if (status === "mergeable") return "merge";
	return "working";
}

function useLaneLabel() {
	const { t } = useTranslation();
	return (lane: Lane) =>
		lane === "working" ? t("zone.working") : lane === "action" ? t("zone.action") : lane === "review" ? t("column.needs_review") : t("zone.merge");
}

/** One glyph per state, all in the line colour: a turning arc while working,
 * a target when it needs you, a ring in review, a dot when ready. */
function StatusGlyph({ status, className }: { status: PreviewCard["status"] | PhoneWorker["status"]; className?: string }) {
	const lane = laneOf(status);
	return (
		<span aria-hidden="true" className={cn("relative grid size-3 shrink-0 place-items-center", TEXT, className)}>
			{lane === "working" ? (
				// The turn is on an HTML wrapper, not the svg: Chromium runs transform
				// animations on SVG elements on the main thread (a style recalc every
				// frame), but composites them on HTML elements.
				<span className="grid size-3 place-items-center will-change-transform motion-safe:animate-spin [animation-duration:1.4s]">
					<svg viewBox="0 0 12 12" className="size-3">
						<circle cx="6" cy="6" r="4.5" fill="none" stroke="var(--schematic-line)" strokeWidth="1" />
						<path d="M6 1.5a4.5 4.5 0 0 1 4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" />
					</svg>
				</span>
			) : lane === "action" ? (
				<>
					<span className="absolute size-3 rounded-full border border-current opacity-60" />
					<span className="size-1 rounded-full bg-current" />
				</>
			) : lane === "review" ? (
				<span className="size-2 rounded-full border border-current" />
			) : (
				<span className="size-1.5 rounded-full bg-current" />
			)}
		</span>
	);
}

/** An agent's brand mark as a flat outline (AgentOutline). */
function AgentMark({ agent, className }: { agent?: string; className?: string }) {
	return <AgentOutline agent={agent} className={cn("size-3.5", className)} />;
}

/** A labelled topbar control: an outlined pill. */
function TopbarAction({ icon, children }: { icon: ReactNode; children?: ReactNode }) {
	return (
		<span className={cn("flex h-6 items-center gap-1.5 rounded-md border px-2 text-[10.5px]", LINE, MUTED)}>
			{icon}
			{children}
		</span>
	);
}

// ── Scene ───────────────────────────────────────────────────────────────────

export function SchematicScene({
	page,
	project,
	cards,
	sidebarCards,
	boardKey,
	runId,
	pileCards,
	pickerAgents,
	selectedAgentId,
	phone,
	phoneShown,
	phoneDelay = 0,
	githubShown,
	focus,
}: {
	page: ScenePage;
	project: string;
	/** The board's cards at this beat of the story. */
	cards: PreviewCard[];
	/** The sessions listed under the project, in task order. */
	sidebarCards: PreviewCard[];
	/** Remounts the board so a new loop's cards arrive again. */
	boardKey: string;
	/** Remounts the chat so each loop replays its exchange. */
	runId: number;
	/** The workers the orchestrator starts, as cards piled up in its chat. */
	pileCards: PreviewCard[];
	pickerAgents: Array<{ id: string; label: string; dimmed?: boolean }>;
	selectedAgentId?: string;
	/** The phone's screen; it slides onto the plane beside the window when shown. */
	phone: ReactNode;
	phoneShown: boolean;
	/** Seconds to hold the phone (and the refit) before it moves. */
	phoneDelay?: number;
	/** The story's CI part: GitHub is in, and pull requests go through it. */
	githubShown: boolean;
	/** What the camera frames: the whole sheet, or the part the story is about. */
	focus: SceneFocus;
}) {
	const reduceMotion = useReducedMotion();
	const callouts = useCallouts();
	const agent = pickerAgents.find((candidate) => candidate.id === selectedAgentId) ?? pickerAgents[0];
	const transition = reduceMotion ? { duration: 0 } : { ...SETTLE, delay: phoneDelay };
	const cameraMove = reduceMotion ? { duration: 0 } : { ...CAMERA_MOVE, delay: phoneDelay };
	// The overview fits every page's callouts at once, so only the phone ever
	// refits it.
	const bracket = stackBracket(pileCards.length);
	const extents: Point[] = [
		...[...callouts.chat, ...callouts.board, ...callouts.new].flatMap(calloutExtent),
		[bracket.leader + 190, bracket.middle],
		[bracket.x, bracket.top],
		// GitHub's mark, its footprint and its top, and its labels beside it.
		...[
			[GITHUB_X, GITHUB_Y],
			[GITHUB_X + GITHUB_SIZE + 130, GITHUB_Y],
			[GITHUB_X + GITHUB_SIZE + 130, GITHUB_Y + GITHUB_SIZE],
			[GITHUB_X, GITHUB_Y + GITHUB_SIZE],
		].flatMap(([x, y]) => [toSheet(PANEL_X + x!, PANEL_Y + y!, PANEL_Z), toSheet(PANEL_X + x!, PANEL_Y + y!, PANEL_Z + ON_GITHUB_LIFT + RAISED_DEPTH)]),
	];
	// Once the phone is in, AO Cloud and the phone carry their own labels and
	// the callouts' balloons would sit over them, so the callouts go.
	const shownCallouts = phoneShown ? [] : callouts[page];
	const overview = frameCamera(
		bounds(overviewPoints(phoneShown, phoneShown ? [...extents, ...cloudPoints(), ...phonePoints()] : extents)),
		FIT_MARGIN,
	);
	// The account step follows its story between AO Cloud and the phone.
	const sync = useSyncStory(phoneShown, phoneDelay);
	const cameraFocus: SceneFocus | CameraFocus =
		focus === "devices" && !reduceMotion ? (sync.phase === "cloud" ? "cloud" : "mobile") : focus;
	// Once your answer reaches the worker waiting on you, it is working again.
	const answered = sync.phase === "resumed";
	// Close in on what the story is about, but not so far the app stops reading as the app.
	const focusFrame = cameraFocus === "overview" ? null : bounds(focusPoints(cameraFocus, pileCards.length));
	const camera = focusFrame ? frameCamera(focusFrame, FOCUS_MARGIN, overview.scale * MAX_FOCUS_ZOOM) : overview;
	return (
		<div className="relative" style={{ width: APP_WIDTH, height: APP_HEIGHT }}>
			{/* Its own compositor layer: the slabs' layered fills are painted once and
			    moved, instead of repainted on every frame of a refit. */}
			<motion.div
				className="absolute left-0 top-0 will-change-transform"
				style={{ originX: 0, originY: 0 }}
				initial={false}
				animate={camera}
				transition={cameraMove}
			>
				<div className="relative" style={{ width: WINDOW_WIDTH, height: WINDOW_HEIGHT, transform: ISO, transformOrigin: "0 0" }}>
					{/* Under the phone and the window, which hide the pipelines' ends. */}
					<AccountGround shown={phoneShown} delay={phoneDelay + 0.6} sync={sync} />
					<motion.div
						className="absolute"
						style={{ left: PHONE_X, top: WINDOW_HEIGHT - PHONE_HEIGHT, width: PHONE_WIDTH, height: PHONE_HEIGHT }}
						initial={false}
						// Lowered onto the sheet: straight down on screen is (-1, +1) on the plane.
						animate={phoneShown ? { opacity: 1, x: 0, y: 0 } : { opacity: 0, x: PHONE_DROP, y: -PHONE_DROP }}
						transition={transition}
					>
						<SyncPhaseContext.Provider value={sync.phase}>
							<Phone>{phone}</Phone>
						</SyncPhaseContext.Provider>
					</motion.div>
					<Slab depth={WINDOW_DEPTH} radius={18} outline className="inset-0">
						<WindowFace
							page={page}
							project={project}
							cards={answered ? cards.map(resumeWaiting) : cards}
							sidebarCards={answered ? sidebarCards.map(resumeWaiting) : sidebarCards}
							boardKey={boardKey}
							runId={runId}
							pileCards={pileCards}
							githubShown={githubShown}
							agents={pickerAgents}
							agent={agent}
						/>
					</Slab>
					<AccountFront shown={phoneShown} delay={phoneDelay + 0.6} sync={sync} />
				</div>
				<AnimatePresence>{page === "chat" ? <StackBracket key={runId} total={pileCards.length} /> : null}</AnimatePresence>
				<AnimatePresence mode="popLayout">
					<motion.div
						key={`${page}-${phoneShown}`}
						className="absolute left-0 top-0"
						initial={false}
						exit={{ opacity: 0, transition: { duration: 0.3, ease: EASE_IN_OUT } }}
					>
						<Callouts callouts={focusFrame ? [] : shownCallouts} />
					</motion.div>
				</AnimatePresence>
			</motion.div>
		</div>
	);
}

// ── Camera ──────────────────────────────────────────────────────────────────

/** What the camera frames at each beat of the story. */
/** "devices" is the account step: it moves between AO Cloud and the phone. */
export type SceneFocus = "overview" | "chat" | "working" | "ci" | "new" | "devices";
/** What the camera can frame: a focus, or one of the account step's two parts. */
type CameraFocus = Exclude<SceneFocus, "overview"> | "cloud" | "mobile";

const CAMERA_MOVE = { duration: 1.3, ease: [0.65, 0, 0.35, 1] } as const;
const FOCUS_MARGIN = 56;
/** How far past the overview the camera may close in. */
const MAX_FOCUS_ZOOM = 2.1;

/** A rectangle on the center panel's face, `z` above it, as sheet points. */
function panelRect(x0: number, y0: number, x1: number, y1: number, z = 0): Point[] {
	return [
		[x0, y0],
		[x1, y0],
		[x0, y1],
		[x1, y1],
	].map(([x, y]) => toSheet(PANEL_X + x!, PANEL_Y + y!, PANEL_Z + z));
}

/** The sheet points each focus frames. */
function focusPoints(focus: CameraFocus, pileCount: number): Point[] {
	switch (focus) {
		case "chat": {
			// The request, the reply, and the stack of workers with its label.
			const bracket = stackBracket(pileCount);
			return [
				...panelRect(30, 66, FACE_WIDTH - 30, 200),
				toSheet(PANEL_X + PILE_X, PANEL_Y + PILE_Y + CARD_HEIGHT, PANEL_Z),
				[bracket.x, bracket.top],
				[bracket.x, bracket.bottom],
				[bracket.leader + 170, bracket.middle],
			];
		}
		case "working":
			// The board, every worker going at once in the first lane.
			return panelRect(0, 44, FACE_WIDTH, CARD_TOP + 4 * (CARD_HEIGHT + CARD_GAP) - CARD_GAP, RAISED_DEPTH);
		case "ci": {
			// Where pull requests end up, and GitHub, where they go first.
			const top = GITHUB_FLOAT + GITHUB_DEPTH;
			return [
				...panelRect(laneX("review") - 12, LANE_TOP - 12, FACE_WIDTH, CARD_TOP + 2 * (CARD_HEIGHT + CARD_GAP), RAISED_DEPTH),
				...panelRect(GITHUB_X, GITHUB_Y, GITHUB_X + GITHUB_SIZE, GITHUB_Y + GITHUB_SIZE, ON_GITHUB_LIFT + RAISED_DEPTH),
				...panelRect(GITHUB_X + GITHUB_SIZE + 36, GITHUB_Y + 30, GITHUB_X + GITHUB_SIZE + 170, GITHUB_Y + 110, top),
			];
		}
		case "new":
			// The composer with its agent picker open.
			return [...panelRect(100, 96, 630, 420), ...panelRect(100, 96, 630, 420, MENU_DEPTH)];
		case "cloud":
			// AO Cloud's platform, its sessions and the pipeline to it.
			return cloudPoints();
		case "mobile":
			// The phone with the cable round to the window, and the board's first
			// two lanes: the worker waiting on you, and Working, where it goes
			// once answered.
			return [
				...phonePoints(),
				...panelRect(laneX("working") - 12, LANE_TOP - 12, laneX("action") + LANE_WIDTH + 12, CARD_TOP + CARD_HEIGHT + 12, RAISED_DEPTH),
			];
		case "devices":
			return [...focusPoints("cloud", pileCount), ...focusPoints("mobile", pileCount)];
	}
}

/** Where the drawing's callouts point on each page (on the plane, raised to
 * the face they mark) and where their balloons sit on the sheet: up and to
 * the left of the window's top edge, or down and to the left of its side. */
const PANEL_X = SIDEBAR_WIDTH;
const PANEL_Y = 10;
const PANEL_Z = WINDOW_DEPTH + PANEL_DEPTH;

function useCallouts(): Record<ScenePage, Callout[]> {
	const { t } = useTranslation();
	const callout = (label: string, x: number, y: number, z: number, dx: number, dy: number): Callout => {
		const at = toSheet(x, y, z);
		return { label, at, balloon: [at[0] + dx, at[1] + dy] };
	};
	const sessions = callout(t("inspector.workers"), 120, 230, WINDOW_DEPTH, -150, 170);
	return {
		chat: [
			callout(t("shell.orchestrator"), PANEL_X + 70, PANEL_Y + 22, PANEL_Z, -120, -110),
		],
		board: [callout(t("shell.board"), PANEL_X + 40, PANEL_Y + 64, PANEL_Z, -120, -110), sessions],
		new: [
			callout(t("agentSwitch.agent"), PANEL_X + 253, PANEL_Y + 175, PANEL_Z + MENU_DEPTH, -150, -200),
			callout(t("shell.projects"), 90, 204, WINDOW_DEPTH, -150, 170),
		],
	};
}

function WindowFace({
	page,
	project,
	cards,
	sidebarCards,
	boardKey,
	runId,
	pileCards,
	githubShown,
	agents,
	agent,
}: {
	page: ScenePage;
	project: string;
	cards: PreviewCard[];
	sidebarCards: PreviewCard[];
	boardKey: string;
	runId: number;
	pileCards: PreviewCard[];
	githubShown: boolean;
	agents: Array<{ id: string; label: string; dimmed?: boolean }>;
	agent?: { id: string; label: string };
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const pages: Record<ScenePage, ReactNode> = {
		chat: <ChatPage key={runId} agent={agent} />,
		board: <BoardPage key={boardKey} cards={cards} />,
		new: <NewChatPage agents={agents} agent={agent} project={project} menuOpen={page === "new"} />,
	};
	return (
		<>
			{/* Titlebar band over the sidebar: traffic lights, sidebar toggle, the app. */}
			<div className="absolute left-0 top-0 flex h-11 items-center gap-2 px-4" style={{ width: SIDEBAR_WIDTH }}>
				{[0, 1, 2].map((dot) => (
					<span key={dot} className={cn("size-[9px] rounded-full border", LINE)} />
				))}
				<PanelLeft className={cn("ml-2 size-3.5", PASSIVE)} {...ICON} />
				<OrchestratorIcon className={cn("ml-1 size-3.5", TEXT)} {...ICON} />
				<span className={cn("text-[11.5px]", TEXT)}>{t("shell.orchestrator")}</span>
			</div>
			<Sidebar page={page} project={project} sessions={sidebarCards} />
			<Slab depth={PANEL_DEPTH} radius={14} style={{ left: SIDEBAR_WIDTH, top: 10, right: 12, bottom: 12 }}>
				{(["chat", "board", "new"] as const).map((candidate) => {
					const shown = candidate === page;
					return (
						<motion.div
							key={candidate}
							className={cn("absolute inset-0 flex flex-col will-change-[opacity]", !shown && "pointer-events-none")}
							initial={false}
							animate={{ opacity: shown ? 1 : 0 }}
							transition={reduceMotion ? { duration: 0 } : PAGE_FADE}
						>
							{pages[candidate]}
						</motion.div>
					);
				})}
				<WorkerCards page={page} cards={page === "chat" ? pileCards : cards} runId={runId} github={githubShown && page === "board"} />
			</Slab>
		</>
	);
}

// ── Sidebar ─────────────────────────────────────────────────────────────────

function Sidebar({ page, project, sessions }: { page: ScenePage; project: string; sessions: PreviewCard[] }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	return (
		<aside className="absolute bottom-0 left-0 top-11 flex flex-col px-3 pb-3 pt-1" style={{ width: SIDEBAR_WIDTH }}>
			<NavRow icon={<Search className="size-3.5" {...ICON} />} trailing="⌘K">
				{t("shell.search")}
			</NavRow>
			<NavRow icon={<CalendarClock className="size-3.5" {...ICON} />}>{t("automations.title")}</NavRow>
			<NavRow icon={<File className="size-3.5" {...ICON} />}>{t("artifacts.title")}</NavRow>
			<SectionRow>{t("shell.projects")}</SectionRow>
			<div className={cn("flex h-8 items-center gap-2.5 px-2 text-[11.5px] transition-colors duration-300", page === "new" ? MUTED : TEXT)}>
				<Folder className="size-3.5" {...ICON} />
				<span className="min-w-0 flex-1 truncate">{project}</span>
				<OrchestratorIcon className={cn("size-3.5", page === "chat" ? TEXT : PASSIVE)} {...ICON} />
				<EllipsisVertical className={cn("size-3.5", PASSIVE)} {...ICON} />
			</div>
			<div className="flex flex-col">
				<AnimatePresence initial={false}>
					{sessions.map((card) => (
						<motion.div
							key={card.id}
							className={cn("flex h-7 items-center gap-2.5 pl-7 pr-2 text-[11px]", MUTED)}
							initial={reduceMotion ? false : { opacity: 0, y: -4 }}
							animate={{ opacity: 1, y: 0 }}
							exit={{ opacity: 0 }}
							transition={RISE}
						>
							<StatusGlyph status={card.status} />
							<span className="min-w-0 flex-1 truncate">{card.title}</span>
							<span className={cn("shrink-0 tabular-nums text-[10px]", PASSIVE)}>{card.minutesAgo}m</span>
						</motion.div>
					))}
				</AnimatePresence>
			</div>
			<SectionRow>{t("standalone.workspaceName")}</SectionRow>
			<div className="mt-auto flex flex-col">
				<NavRow icon={<LogIn className="size-3.5" {...ICON} />}>{t("shell.signInToAOCloud")}</NavRow>
				<NavRow icon={<Smartphone className="size-3.5" {...ICON} />}>{t("settings.connectMobile")}</NavRow>
				<NavRow icon={<Settings className="size-3.5" {...ICON} />} trailing={<Coffee className="size-3.5" {...ICON} />}>
					{t("shell.settings")}
				</NavRow>
			</div>
		</aside>
	);
}

function NavRow({ icon, trailing, children }: { icon: ReactNode; trailing?: ReactNode; children: ReactNode }) {
	return (
		<div className={cn("flex h-8 items-center gap-2.5 px-2 text-[11.5px]", MUTED)}>
			{icon}
			<span className="min-w-0 flex-1 truncate">{children}</span>
			{trailing ? <span className={cn("shrink-0 text-[10px]", PASSIVE)}>{trailing}</span> : null}
		</div>
	);
}

function SectionRow({ children }: { children: ReactNode }) {
	return (
		<div className={cn("mt-3 flex h-8 items-center gap-1.5 px-2 text-[11.5px]", MUTED)}>
			<span>{children}</span>
			<ChevronDown className={cn("size-3", PASSIVE)} {...ICON} />
			<Plus className={cn("ml-auto size-3.5", PASSIVE)} {...ICON} />
		</div>
	);
}

// ── Center panel pages ──────────────────────────────────────────────────────

function Topbar({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
	return (
		<header className={cn("flex h-11 shrink-0 items-center gap-3 border-b px-4", LINE)}>
			<div className="flex min-w-0 flex-1 items-center gap-2 self-stretch">{children}</div>
			<div className={cn("flex shrink-0 items-center gap-2.5", MUTED)}>{actions}</div>
		</header>
	);
}

function Composer({
	placeholder,
	agent,
	contextMeter = false,
	className,
	style,
}: {
	placeholder: string;
	agent?: { id: string; label: string };
	/** A session chat's context meter before the agent; New chat has none. */
	contextMeter?: boolean;
	className?: string;
	style?: CSSProperties;
}) {
	const { t } = useTranslation();
	return (
		<Slab depth={RAISED_DEPTH} radius={12} className={className} style={style}>
			<div className="flex size-full flex-col justify-between px-4 py-3">
				<span className={cn("text-[11.5px]", PASSIVE)}>{placeholder}</span>
				<div className={cn("flex items-center gap-3 text-[10.5px]", MUTED)}>
					<Plus className="size-3.5" {...ICON} />
					{contextMeter ? (
						<svg aria-hidden="true" viewBox="0 0 14 14" className="size-3.5">
							<circle cx="7" cy="7" r="5.5" fill="none" stroke="var(--schematic-line)" strokeWidth="1" />
							<path d="M7 1.5a5.5 5.5 0 0 1 5.2 3.7" fill="none" stroke="currentColor" strokeWidth="1" />
						</svg>
					) : null}
					<span className="flex items-center gap-1.5">
						<AgentMark agent={agent?.id} />
						<span className={TEXT}>{agent?.label ?? t("agentSwitch.agent")}</span>
						<span className={PASSIVE}>{t("settings.models.effortLevel.high")}</span>
					</span>
					<span className="ml-auto flex items-center gap-1.5">
						<ShieldCheck className="size-3.5" {...ICON} />
						{t("settings.project.permissionAuto")}
					</span>
					<span className={cn("grid size-6 place-items-center rounded-full border", LINE, TEXT)}>
						<ArrowUp className="size-3" {...ICON} />
					</span>
				</div>
			</div>
		</Slab>
	);
}

/** The orchestrator's chat: the request, and the plan fanning out to workers. */
function ChatPage({ agent }: { agent?: { id: string; label: string } }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const enter = (delay: number) =>
		reduceMotion ? {} : { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, transition: { ...RISE, delay } };
	return (
		<>
			<Topbar
				actions={
					<>
						<TopbarAction icon={<Plus className="size-3" {...ICON} />}>{t("newTask.task")}</TopbarAction>
						<TopbarAction icon={<LayoutDashboard className="size-3" {...ICON} />}>{t("shell.board")}</TopbarAction>
						<Code2 className="size-3.5" {...ICON} />
						<Play className="size-3.5" {...ICON} />
						<PanelRight className="size-3.5" {...ICON} />
						<Bell className="size-3.5" {...ICON} />
					</>
				}
			>
				{/* The open tab, underlined where it meets the topbar's rule. */}
				<span className={cn("relative flex items-center gap-2 self-stretch pr-1 text-[11.5px]", TEXT)}>
					<AgentMark agent={agent?.id} />
					{t("shell.orchestrator")}
					<span aria-hidden="true" className="absolute inset-x-0 -bottom-px border-b border-(--schematic-text)" />
				</span>
			</Topbar>
			<div className="relative flex min-h-0 flex-1 flex-col px-10 pt-8">
				<motion.p {...enter(0.1)} className={cn("max-w-[400px] self-end rounded-[12px] border px-4 py-3 text-[11.5px] leading-relaxed", LINE, TEXT)}>
					{t("firstRun.exampleMessage")}
				</motion.p>
				<motion.div {...enter(0.7)} className={cn("mt-7 flex items-center gap-2.5 text-[11.5px]", MUTED)}>
					<OrchestratorIcon className={cn("size-3.5", TEXT)} {...ICON} />
					{t("firstRun.exampleReply")}
				</motion.div>
				{/* The workers it starts stack up on the right (WorkerCards, StackBracket). */}
			</div>
			<div className="relative h-[124px] shrink-0">
				<Composer placeholder={t("sideChat.messageTheAgent")} agent={agent} contextMeter style={{ left: 28, right: 34, top: 10, bottom: 22 }} />
			</div>
		</>
	);
}

// The board's grid, shared by its lane headers and the card layer so a card
// lands exactly under its lane. Center panel face coordinates.
const FACE_WIDTH = WINDOW_WIDTH - SIDEBAR_WIDTH - 12 - 2;
const LANE_PAD = 16;
const LANE_GAP = 12;
const LANE_WIDTH = (FACE_WIDTH - 2 * LANE_PAD - 3 * LANE_GAP) / 4;
const LANE_TOP = 44 + 18;
const CARD_TOP = LANE_TOP + 30;
const CARD_HEIGHT = 118;
const laneX = (lane: Lane) => LANE_PAD + LANES.indexOf(lane) * (LANE_WIDTH + LANE_GAP);

// The pile in the orchestrator's chat: an exploded stack, each card straight
// above the last with clear air between them. It stands low in the chat,
// clear of the request and the reply above it.
const PILE_X = 300;
const PILE_Y = 380;
const PILE_STEP = 64;
const pileSpot = (index: number) => ({ x: PILE_X + index * PILE_STEP, y: PILE_Y - index * PILE_STEP });
/** When the pile's cards start down, after the orchestrator's reply, and when each lands. */
const pileDrop = (index: number) => 1 + index * 0.32;
const pileLanding = (index: number) => pileDrop(index) + 0.5;

/** The stack's upright bracket on the sheet, right of the stack: its x, top,
 * bottom, middle, and where the leader ends. */
function stackBracket(total: number) {
	const points = Array.from({ length: total }, (_, index) => {
		const spot = pileSpot(index);
		const at = (u: number, v: number, z: number) => toSheet(PANEL_X + spot.x + u, PANEL_Y + spot.y + v, PANEL_Z + z);
		return [at(LANE_WIDTH, 0, RAISED_DEPTH), at(LANE_WIDTH, CARD_HEIGHT, RAISED_DEPTH), at(0, CARD_HEIGHT, 0)];
	}).flat();
	const right = Math.max(...points.map(([x]) => x));
	const top = Math.min(...points.map(([, y]) => y));
	const bottom = Math.max(...points.map(([, y]) => y));
	const x = right + 18;
	return { x, top, bottom, middle: (top + bottom) / 2, leader: x + 46 };
}

/**
 * The stack's annotation while the orchestrator starts its workers: an
 * upright bracket spanning the stack, a leader out to the right, and the count
 * of workers set down so far, then a check once they all have.
 */
function StackBracket({ total }: { total: number }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [landed, setLanded] = useState(reduceMotion ? total : 0);
	useEffect(() => {
		if (reduceMotion) return;
		const timers = Array.from({ length: total }, (_, index) =>
			window.setTimeout(() => setLanded(index + 1), pileLanding(index) * 1000),
		);
		return () => timers.forEach((timer) => window.clearTimeout(timer));
	}, [reduceMotion, total]);
	const done = landed >= total;
	const { x, top, bottom, middle, leader } = stackBracket(total);
	const stroke = { stroke: "var(--schematic-line-strong)", strokeWidth: 1, vectorEffect: "non-scaling-stroke" as const, fill: "none" };
	return (
		<motion.div
			aria-hidden="true"
			className="absolute left-0 top-0"
			initial={reduceMotion ? false : { opacity: 0 }}
			animate={{ opacity: 1 }}
			exit={{ opacity: 0 }}
			transition={{ ...RISE, delay: reduceMotion ? 0 : pileDrop(0) - 0.2 }}
		>
			<svg className="absolute left-0 top-0 overflow-visible" width={1} height={1}>
				<path d={`M ${x - 9} ${top} L ${x} ${top} L ${x} ${bottom} L ${x - 9} ${bottom}`} {...stroke} />
				<line x1={x} y1={middle} x2={leader} y2={middle} {...stroke} />
			</svg>
			<span
				className={cn("absolute flex -translate-y-1/2 items-center gap-2 whitespace-nowrap pl-2 text-[11px] tabular-nums", done ? MUTED : TEXT)}
				style={{ left: leader, top: middle }}
			>
				{done ? <Check className="size-3" {...ICON} /> : <StatusGlyph status="working" />}
				{done ? t("firstRun.workersReady", { total }) : t("firstRun.creatingWorkers", { current: landed, total })}
			</span>
		</motion.div>
	);
}

/** The board: its topbar and the four lane headers; the cards are WorkerCards. */
function BoardPage({ cards }: { cards: PreviewCard[] }) {
	const { t } = useTranslation();
	const laneLabel = useLaneLabel();
	return (
		<>
			<Topbar
				actions={
					<>
						<TopbarAction icon={<Plus className="size-3" {...ICON} />}>{t("newTask.task")}</TopbarAction>
						<TopbarAction icon={<OrchestratorIcon className="size-3" {...ICON} />}>{t("shell.orchestrator")}</TopbarAction>
					</>
				}
			>
				<span className={cn("text-[12px]", TEXT)}>{t("shell.board")}</span>
			</Topbar>
			{LANES.map((lane) => (
				<div
					key={lane}
					className={cn("absolute flex items-center gap-2 px-1 text-[10.5px]", MUTED)}
					style={{ left: laneX(lane), top: LANE_TOP, width: LANE_WIDTH }}
				>
					<span className="truncate">{laneLabel(lane)}</span>
					<span className={cn("tabular-nums", PASSIVE)}>{cards.filter((card) => laneOf(card.status) === lane).length}</span>
				</div>
			))}
		</>
	);
}

const CARD_GAP = 14;
/** How far above the pile each card starts before it is set down on it. */
const CARD_DROP = 64;

// GitHub, floating outside the window's right edge (center panel face
// coordinates): its mark as an extruded outline, hovering. Pull requests are
// handed to it for CI and code review, then handed back.
const GITHUB_X = FACE_WIDTH + 96;
const GITHUB_Y = 80;
const GITHUB_SIZE = 150;
const GITHUB_DEPTH = 14;
/** How high it hovers over its footprint. */
const GITHUB_FLOAT = 60;
/** Where a card hovers while it is on GitHub: centred over the mark, just above it. */
const ON_GITHUB_LIFT = GITHUB_FLOAT + GITHUB_DEPTH + 16;
const ON_GITHUB = {
	x: GITHUB_X + ON_GITHUB_LIFT + (GITHUB_SIZE - LANE_WIDTH) / 2,
	y: GITHUB_Y - ON_GITHUB_LIFT + (GITHUB_SIZE - CARD_HEIGHT) / 2,
};
/** A pull request's round trip: up to GitHub, held for its checks, back down. */
const ROUND_TRIP_S = 2;
const ROUND_TRIP_TIMES = [0, 0.32, 0.68, 1];
/** When, into the trip, CI and then review finish. */
const CI_DONE_S = 0.95;
const REVIEW_DONE_S = 1.3;

type CardMove = { key: string; animate: TargetAndTransition; transition: Transition; viaGitHub: boolean };

/**
 * The workers as cards, one layer over every page so the same card travels:
 * set down one by one onto a pile in the orchestrator's chat as it starts
 * them, then dealt out from the pile into the board's lanes. A worker whose
 * pull request is going to review or merge takes it to GitHub first: the card
 * rises onto the floating GitHub tile, its CI and review run, and it comes
 * back down into its new lane. Each new loop starts a fresh pile: the board's
 * cards fade where they are rather than flying back across each other.
 * Hidden on New chat.
 */
function WorkerCards({ page, cards, runId, github }: { page: ScenePage; cards: PreviewCard[]; runId: number; github: boolean }) {
	const reduceMotion = useReducedMotion();
	const previousPage = useRef(page);
	const pageChanged = previousPage.current !== page;
	const moves = useRef(new Map<string, CardMove>());
	const lanes = useRef(new Map<string, Lane>());
	const [visit, setVisit] = useState<GitHubVisit | null>(null);
	const slots = new Map<string, { x: number; y: number }>();
	if (page === "chat") {
		cards.forEach((card, index) => slots.set(card.id, pileSpot(index)));
	} else {
		const rows = new Map<Lane, number>();
		for (const card of cards) {
			const lane = laneOf(card.status);
			const row = rows.get(lane) ?? 0;
			rows.set(lane, row + 1);
			slots.set(card.id, { x: laneX(lane), y: CARD_TOP + row * (CARD_HEIGHT + CARD_GAP) });
		}
	}
	const opacity = page === "new" ? 0 : 1;
	const starting: PreviewCard[] = [];
	const plan = cards.map((card, index) => {
		const slot = slots.get(card.id)!;
		const lane = laneOf(card.status);
		const key = `${runId}:${page}:${lane}:${slot.x}:${slot.y}:${opacity}`;
		const kept = moves.current.get(card.id);
		// Same destination as last time: hand motion the same target, so a trip
		// in progress is never restarted by the story's next beat.
		if (kept && kept.key === key) return { card, slot, move: kept };
		const before = lanes.current.get(card.id);
		const toGitHub =
			github && !reduceMotion && !pageChanged && before === "working" && (lane === "review" || lane === "merge");
		let move: CardMove;
		if (toGitHub) {
			starting.push(card);
			const from = kept?.animate as { x: number | number[]; y: number | number[] } | undefined;
			const fromX = Array.isArray(from?.x) ? from.x[from.x.length - 1]! : (from?.x ?? slot.x);
			const fromY = Array.isArray(from?.y) ? from.y[from.y.length - 1]! : (from?.y ?? slot.y);
			move = {
				key,
				viaGitHub: true,
				animate: { x: [fromX, ON_GITHUB.x, ON_GITHUB.x, slot.x], y: [fromY, ON_GITHUB.y, ON_GITHUB.y, slot.y], opacity },
				transition: { duration: ROUND_TRIP_S, ease: [...EASE_IN_OUT], times: ROUND_TRIP_TIMES },
			};
		} else {
			// The pile builds after the orchestrator's reply, a card at a time;
			// dealing onto the board fans out card by card; a lane change goes at once.
			const delay = page === "chat" ? pileDrop(index) : pageChanged ? index * 0.08 : 0;
			move = { key, viaGitHub: false, animate: { ...slot, opacity }, transition: reduceMotion ? { duration: 0 } : { ...SETTLE, delay } };
		}
		return { card, slot, move };
	});

	useEffect(() => {
		previousPage.current = page;
		for (const { card, move } of plan) {
			moves.current.set(card.id, move);
			lanes.current.set(card.id, laneOf(card.status));
		}
		const sent = starting[0];
		if (sent) setVisit({ pr: sent.pr, at: Date.now(), approved: laneOf(sent.status) === "merge" });
	});

	return (
		<div className="pointer-events-none absolute inset-0">
			<GitHubSkeleton shown={github} visit={visit} />
			<AnimatePresence>
				{plan.map(({ card, slot, move }, index) => (
					<motion.div
						// A new loop's cards are new cards, so they arrive instead of travelling back.
						key={`${runId}:${card.id}`}
						className="absolute left-0 top-0 will-change-transform"
						// A card on its way to GitHub travels over everything else.
						style={{ width: LANE_WIDTH, height: CARD_HEIGHT, zIndex: move.viaGitHub ? 50 : index + 1 }}
						// Set down onto its spot from straight above it.
						initial={reduceMotion ? false : { x: slot.x + CARD_DROP, y: slot.y - CARD_DROP, opacity: 0 }}
						animate={move.animate}
						exit={{ opacity: 0, transition: { duration: 0.35, ease: EASE_IN_OUT } }}
						transition={move.transition}
					>
						<WorkerCard card={card} />
					</motion.div>
				))}
			</AnimatePresence>
		</div>
	);
}

type CheckState = "idle" | "running" | "passed";
/** A pull request on GitHub: when it arrived, and whether its review passes
 * (bound for Merge) or is still pending when it leaves (bound for Review). */
type GitHubVisit = { pr?: number; at: number; approved: boolean };

/** A check's state `elapsed` seconds into a pull request's visit. */
function checkState(elapsed: number | null, doneAt: number): CheckState {
	if (elapsed === null || elapsed > ROUND_TRIP_S + 0.6) return "idle";
	if (elapsed < ROUND_TRIP_TIMES[1]! * ROUND_TRIP_S) return "idle";
	return elapsed < doneAt ? "running" : "passed";
}

/** GitHub's mark as a line: one outline of its shape, at height `lift`. */
function GitHubOutline({ lift, className, filled = false, stroked = true }: { lift: number; className?: string; filled?: boolean; stroked?: boolean }) {
	return (
		<svg
			aria-hidden="true"
			viewBox="0 0 24 24"
			className={cn("absolute overflow-visible", className)}
			style={{ left: GITHUB_X + lift, top: GITHUB_Y - lift, width: GITHUB_SIZE, height: GITHUB_SIZE }}
		>
			<path
				d={GITHUB_MARK_PATH}
				fill={filled ? "var(--schematic-solid)" : "none"}
				stroke={stroked ? "currentColor" : "none"}
				strokeWidth={1}
				vectorEffect="non-scaling-stroke"
			/>
		</svg>
	);
}

/**
 * GitHub beside the window, as its mark made a solid isometric plate,
 * hovering. The pull request's CI and code review are
 * labels beside it, and run while a card is on it. Shown only in the story's
 * CI part.
 */
function GitHubSkeleton({ shown, visit }: { shown: boolean; visit: GitHubVisit | null }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!visit) return;
		// Re-render only when a check changes state (checkState's thresholds),
		// not on a fast tick: the labels have four moments per round trip.
		const moments = [ROUND_TRIP_TIMES[1]! * ROUND_TRIP_S, CI_DONE_S, REVIEW_DONE_S, ROUND_TRIP_S + 0.6];
		const timers = moments.map((at) =>
			window.setTimeout(() => setNow(Date.now()), Math.max(0, visit.at + at * 1000 - Date.now()) + 20),
		);
		setNow(Date.now());
		return () => timers.forEach((timer) => window.clearTimeout(timer));
	}, [visit]);
	const elapsed = visit ? (now - visit.at) / 1000 : null;
	const ci = checkState(elapsed, CI_DONE_S);
	const review = checkState(elapsed, visit?.approved === false ? Number.POSITIVE_INFINITY : REVIEW_DONE_S);
	const busy = ci !== "idle";
	const top = GITHUB_FLOAT + GITHUB_DEPTH;
	return (
		<motion.div
			className="absolute inset-0"
			initial={false}
			animate={{ opacity: shown ? 1 : 0 }}
			transition={reduceMotion ? { duration: 0 } : PAGE_FADE}
		>
			{/* The mark as a solid plate: its base outline, its body filled a layer
			    at a time, and its top face, filled and outlined, over it. */}
			<GitHubOutline lift={GITHUB_FLOAT} filled className="text-(--schematic-line)" />
			{Array.from({ length: GITHUB_DEPTH - 1 }, (_, layer) => (
				<GitHubOutline key={layer} lift={GITHUB_FLOAT + layer + 1} filled stroked={false} />
			))}
			<GitHubOutline
				lift={top}
				filled
				className={cn("transition-colors duration-300", busy ? "text-(--schematic-text)" : "text-(--schematic-text-muted)")}
			/>
			{/* Its checks, beside it at its own height. */}
			<div className="absolute flex flex-col gap-2" style={{ left: GITHUB_X + GITHUB_SIZE + 36 + top, top: GITHUB_Y + 42 - top }}>
				<span className={cn("flex items-center gap-2 whitespace-nowrap text-[12px]", TEXT)}>
					{t("report.github")}
					{visit?.pr && busy ? <span className={cn("text-[10.5px] tabular-nums", PASSIVE)}>#{visit.pr}</span> : null}
				</span>
				<CheckRow label={t("pr.section.ci")} state={ci} />
				<CheckRow label={t("pr.section.review")} state={review} />
			</div>
		</motion.div>
	);
}

function CheckRow({ label, state }: { label: string; state: CheckState }) {
	return (
		<div className={cn("flex h-5 items-center gap-2 whitespace-nowrap text-[10.5px] transition-colors duration-300", state === "idle" ? PASSIVE : TEXT)}>
			{state === "running" ? (
				<StatusGlyph status="working" />
			) : state === "passed" ? (
				<Check className="size-3" {...ICON} />
			) : (
				<span aria-hidden="true" className={cn("size-3 rounded-full border", LINE)} />
			)}
			{label}
		</div>
	);
}

/**
 * A session as a card, on the desktop's board and on AO Cloud alike: a thick
 * plate with its screen inset in the top face; on the screen, where it runs,
 * the task, what the agent is doing, its change so far as a diff under the
 * file it is in, and the size of the change.
 */
function DetailCard({
	depth,
	radius,
	header,
	title,
	activity,
	file,
	diff,
	added,
	removed,
	trailing,
}: {
	depth: number;
	radius: number;
	header: ReactNode;
	title: string;
	activity: ReactNode;
	file?: string;
	diff?: PreviewCard["diff"];
	added?: number;
	removed?: number;
	trailing?: ReactNode;
}) {
	return (
		<Slab depth={depth} radius={radius} solid outline className="inset-0">
			<div
				className={cn("absolute inset-[4px] flex flex-col gap-[3px] overflow-hidden border px-2.5 py-[7px]", LINE)}
				style={{ borderRadius: radius - 3 }}
			>
				<div className={cn("flex h-3 items-center gap-1.5 text-[9px]", PASSIVE)}>{header}</div>
				<p className={cn("truncate text-[11px] font-semibold leading-[14px]", TEXT)}>{title}</p>
				<div className={cn("flex h-3 min-w-0 items-center gap-1.5 text-[9.5px]", MUTED)}>{activity}</div>
				<div className={cn("flex min-h-0 flex-1 flex-col overflow-hidden rounded-[5px] border text-[8px] leading-[10px]", LINE)}>
					{file ? (
						<div className={cn("flex shrink-0 items-center gap-1 border-b px-1.5 py-px", LINE, PASSIVE)}>
							<File className="size-2" {...ICON} />
							<span className="truncate">{file}</span>
						</div>
					) : null}
					{diff?.map(([sign, line]) => (
						<div
							key={line}
							className={cn(
								"flex shrink-0 gap-1.5 whitespace-pre px-1.5",
								sign === "+" ? "bg-(--schematic-add-tint) text-(--schematic-add)" : "bg-(--schematic-del-tint) text-(--schematic-del)",
							)}
						>
							<span>{sign}</span>
							<span className="truncate">{line}</span>
						</div>
					))}
				</div>
				<div className={cn("flex h-2.5 items-center gap-2 text-[8.5px] tabular-nums", PASSIVE)}>
					{added !== undefined ? <span className="text-(--schematic-add)">+{added}</span> : null}
					{removed !== undefined ? <span className="text-(--schematic-del)">−{removed}</span> : null}
					<span className="ml-auto truncate">{trailing}</span>
				</div>
			</div>
		</Slab>
	);
}

/** What a working agent is doing: the file it is in, live. */
function EditingLine({ file }: { file: string }) {
	const { t } = useTranslation();
	return (
		<span className="flex min-w-0 items-center gap-1.5 text-(--schematic-live)">
			<StatusGlyph status="working" className="text-(--schematic-live)" />
			<span className="truncate">{t("firstRun.sandboxEditing", { file })}</span>
		</span>
	);
}

/** A worker on the board as the detailed card: its branch and pull request,
 * the task, and what it is doing (the file it is in), asking you (its
 * question) or where it stands. */
function WorkerCard({ card }: { card: PreviewCard }) {
	const laneLabel = useLaneLabel();
	const lane = laneOf(card.status);
	const activity =
		lane === "working" && card.file ? (
			<EditingLine file={card.file} />
		) : (
			<>
				<StatusGlyph status={card.status} />
				<span className={cn("min-w-0 truncate", lane === "action" ? TEXT : MUTED)}>
					{lane === "action" && card.question ? card.question : (card.displayStatus ?? laneLabel(lane))}
				</span>
			</>
		);
	return (
		<DetailCard
			depth={RAISED_DEPTH}
			radius={11}
			header={
				<>
					<AgentMark agent={card.provider} className="size-2.5" />
					<span className="min-w-0 flex-1 truncate">{card.branch}</span>
					{card.pr ? <span className="shrink-0 tabular-nums">#{card.pr}</span> : null}
				</>
			}
			title={card.title}
			activity={activity}
			file={card.file}
			diff={card.diff}
			added={card.added}
			removed={card.removed}
			trailing={`${card.minutesAgo}m`}
		/>
	);
}

/** The picker menu's place over the composer: its left edge on the agent chip
 * (past the composer's padding and the +), its bottom just above the
 * composer's bottom bar. */
const MENU_LEFT = 36;
const MENU_BOTTOM = 40;
const MENU_OPEN = { opacity: 1, y: 0 };
const MENU_CLOSED = { opacity: 0, y: 6 };

/** New chat: the empty chat with its composer and the agent picker open. */
function NewChatPage({
	agents,
	agent,
	project,
	menuOpen,
}: {
	agents: Array<{ id: string; label: string; dimmed?: boolean }>;
	agent?: { id: string; label: string };
	project: string;
	menuOpen: boolean;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const chips = [t("chat.welcome.chip.review"), t("chat.welcome.chip.tests"), t("chat.welcome.chip.trace"), t("chat.welcome.chip.simplify")];
	const menuHeight = 44 + agents.length * 30 + 36;
	return (
		<>
			<Topbar>
				<span className={cn("text-[12px]", TEXT)}>{t("shell.newTask")}</span>
			</Topbar>
			<div className="relative flex min-h-0 flex-1 flex-col items-center px-14 pt-[120px]">
				<p className={cn("text-[14px]", TEXT)}>{t("chat.welcome.heading")}</p>
				<div className="relative mt-6 h-[100px] w-full max-w-[500px]">
					<Composer placeholder={t("firstRun.exampleComposerDraft")} agent={agent} className="inset-0" />
					{/* Like the real picker (side "top", align "start"): it rises from the
					    agent chip in the composer's bottom bar, over the composer. */}
					<motion.div
						className="absolute w-[230px]"
						style={{ left: MENU_LEFT, bottom: MENU_BOTTOM, height: menuHeight }}
						initial={false}
						animate={menuOpen ? MENU_OPEN : MENU_CLOSED}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: EASE_OUT, delay: menuOpen ? 0.35 : 0 }}
					>
						<Slab depth={MENU_DEPTH} radius={11} className="inset-0">
							<div className="flex size-full flex-col py-1.5">
								<div className={cn("mx-1.5 mb-1 flex h-8 items-center gap-2 border-b px-2.5 text-[10.5px]", LINE, PASSIVE)}>
									<Search className="size-3" {...ICON} />
									{t("agentSwitch.search")}
								</div>
								{agents.map((candidate) => (
									<div key={candidate.id} className={cn("flex h-[30px] items-center gap-2 px-4 text-[11px]", candidate.dimmed ? PASSIVE : TEXT)}>
										<AgentMark agent={candidate.id} className="size-3" />
										<span className="min-w-0 flex-1 truncate">{candidate.label}</span>
										{candidate.id === agent?.id ? <Check className="size-3" {...ICON} /> : null}
									</div>
								))}
								<div className={cn("mx-1.5 mt-auto flex h-8 items-center border-t px-2.5 text-[10.5px]", LINE, MUTED)}>{t("agentSelector.manage")}</div>
							</div>
						</Slab>
					</motion.div>
				</div>
				<div className={cn("mt-4 flex items-center gap-2 text-[10.5px]", PASSIVE)}>
					<Folder className="size-3" {...ICON} />
					{project}
				</div>
				<div className="mt-6 flex flex-wrap justify-center gap-2">
					{chips.map((chip) => (
						<span key={chip} className={cn("rounded-full border px-3 py-1 text-[10.5px]", LINE, MUTED)}>
							{chip}
						</span>
					))}
				</div>
			</div>
		</>
	);
}

// ── Phone ───────────────────────────────────────────────────────────────────

/** How far above its spot the phone starts before it is lowered into place. */
const PHONE_DROP = 70;

/** The side buttons on the phone's visible long edge, as fractions of its
 * height: the action button, then volume up and down. */
const PHONE_BUTTONS: Array<[number, number]> = [
	[0.17, 0.05],
	[0.25, 0.085],
	[0.355, 0.085],
];
const BUTTON_PROUD = 3;

/** The phone as a thick slab on the same plane: body with a seam round its
 * middle, side buttons proud of the edge, an evenly inset display with a few
 * glints on the glass, and its screen. */
function Phone({ children }: { children: ReactNode }) {
	const displayWidth = PHONE_WIDTH - 2 * PHONE_BEZEL;
	const scale = displayWidth / PHONE_SCREEN_WIDTH;
	// The buttons sit across the middle third of the edge's height.
	const buttonBase = PHONE_DEPTH * 0.32;
	const buttonDepth = PHONE_DEPTH * 0.36;
	return (
		<>
			<Slab depth={PHONE_DEPTH} radius={PHONE_RADIUS} outline seams={[PHONE_DEPTH / 2]} className="inset-0">
				<div
					className={cn("absolute overflow-hidden border", LINE)}
					style={{ inset: PHONE_BEZEL, borderRadius: PHONE_RADIUS - PHONE_BEZEL }}
				>
					<div
						className="absolute left-0 top-0 origin-top-left"
						style={{ width: PHONE_SCREEN_WIDTH, height: (PHONE_HEIGHT - 2 * PHONE_BEZEL) / scale, transform: `scale(${scale})` }}
					>
						{children}
					</div>
					{/* Glass: three short parallel glints in the top corner. */}
					{[0, 1, 2].map((glint) => (
						<span
							key={glint}
							aria-hidden="true"
							className={cn("absolute h-0 origin-left -rotate-45 border-t", LINE)}
							style={{ right: 18 + glint * 9, top: 34 + glint * 9, width: glint === 1 ? 26 : 16 }}
						/>
					))}
				</div>
			</Slab>
			{PHONE_BUTTONS.map(([at, length]) => (
				<Slab
					key={at}
					depth={buttonDepth}
					radius={1.5}
					outline
					style={{
						left: -BUTTON_PROUD + buttonBase,
						top: PHONE_HEIGHT * at - buttonBase,
						width: BUTTON_PROUD + 1,
						height: PHONE_HEIGHT * length,
					}}
				/>
			))}
		</>
	);
}

/** The mobile app's Workers tab (packages/mobile/app/(tabs)/index.tsx) as a
 * line drawing, at the phone's logical width: the header buttons, the large
 * title, Pinned and Recent, each worker as project and time, title, then its
 * branch or pull request, and the floating tab bar. */
export function SchematicPhoneScreen({ project, workers }: { project: string; workers: PhoneWorker[] }) {
	const { t } = useTranslation();
	const phase = useContext(SyncPhaseContext);
	const pinned = workers.filter((worker) => worker.status === "needs_input");
	const recent = workers.filter((worker) => worker.status !== "needs_input");
	// The account story: the waiting worker's question arrives as a notification,
	// you answer in it, and once the answer is sent the worker is no longer waiting.
	const asking = pinned[0];
	const notified = phase === "asked" || phase === "replying";
	const answered = phase === "answering" || phase === "resumed";
	const headerButton = (icon: ReactNode) => (
		<span className={cn("grid size-11 place-items-center rounded-full border", LINE, TEXT)}>{icon}</span>
	);
	return (
		<div className="relative flex size-full flex-col">
			<span className={cn("absolute left-1/2 top-[11px] h-[37px] w-[126px] -translate-x-1/2 rounded-full border", LINE)} />
			<div className="flex items-center justify-between px-4 pt-[62px]">
				{headerButton(<ListFilter className="size-5" {...ICON} />)}
				<div className="flex items-center gap-2">
					{headerButton(<Cog className="size-5" {...ICON} />)}
					{headerButton(<OrchestratorIcon className="size-5" {...ICON} />)}
					{headerButton(<Bell className="size-5" {...ICON} />)}
				</div>
			</div>
			<p className={cn("px-5 pt-5 text-[34px] font-semibold leading-tight tracking-tight", TEXT)}>{t("inspector.workers")}</p>
			<PhoneSection label={t("shell.pinned")}>
				{pinned.map((worker) => (
					<PhoneWorkerRow key={worker.id} worker={worker} project={project} highlighted={!answered} />
				))}
			</PhoneSection>
			<PhoneSection label={t("firstRun.phoneRecent")}>
				{recent.map((worker) => (
					<PhoneWorkerRow key={worker.id} worker={worker} project={project} />
				))}
			</PhoneSection>
			{/* The floating tab bar, Workers selected. */}
			<div className={cn("absolute inset-x-[60px] bottom-[30px] flex h-[66px] items-center justify-between rounded-full border px-1.5", LINE)}>
				{[
					{ icon: <Layers className="size-6" {...ICON} />, label: t("inspector.workers"), active: true },
					{ icon: <Folder className="size-6" {...ICON} />, label: t("shell.projects"), active: false },
					{ icon: <SquarePen className="size-6" {...ICON} />, label: t("firstRun.phoneCompose"), active: false },
				].map((tab) => (
					<span
						key={tab.label}
						className={cn(
							"flex h-[54px] w-[86px] flex-col items-center justify-center gap-1 rounded-full text-[11px]",
							tab.active ? cn("border", LINE, TEXT) : MUTED,
						)}
					>
						{tab.icon}
						{tab.label}
					</span>
				))}
			</div>
			<AnimatePresence>
				{asking && notified ? (
					<motion.div
						key="notification"
						className={cn("absolute inset-x-[10px] top-[54px] flex flex-col gap-2.5 rounded-[26px] border px-4 pb-3 pt-3.5", OUTLINE, FACE)}
						initial={{ opacity: 0, y: -28 }}
						animate={{ opacity: 1, y: 0 }}
						exit={{ opacity: 0, y: -28 }}
						transition={{ duration: 0.45, ease: EASE_OUT }}
					>
						<div className={cn("flex items-center gap-2 text-[13px]", MUTED)}>
							<span className={cn("grid size-6 place-items-center rounded-[7px] border", LINE, TEXT)}>
								<OrchestratorIcon className="size-3.5" {...ICON} />
							</span>
							<span className="min-w-0 flex-1 truncate">{project}</span>
							<span className={PASSIVE}>{t("firstRun.phoneNeedsInput")}</span>
						</div>
						<div className="flex flex-col gap-0.5">
							<p className={cn("truncate text-[15px] font-semibold", TEXT)}>{asking.title}</p>
							<p className={cn("text-[15px] leading-snug", TEXT)}>{t("firstRun.syncQuestion")}</p>
						</div>
						{/* The reply field: empty until you answer, then your answer, sent. */}
						<div className={cn("flex h-11 items-center gap-2 rounded-full border pl-4 pr-1.5", LINE)}>
							<span
								className={cn(
									"min-w-0 flex-1 truncate text-[15px] transition-opacity duration-300",
									TEXT,
									phase === "replying" ? "opacity-100" : "opacity-0",
								)}
							>
								{t("firstRun.syncAnswer")}
							</span>
							<span
								className={cn(
									"grid size-8 place-items-center rounded-full border transition-colors duration-300",
									phase === "replying" ? cn(OUTLINE, TEXT) : cn(LINE, PASSIVE),
								)}
							>
								<ArrowUp className="size-4" {...ICON} />
							</span>
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

function PhoneSection({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex flex-col px-2 pt-6">
			<p className={cn("px-3 pb-2 text-[13px]", PASSIVE)}>{label}</p>
			{children}
		</div>
	);
}

function PhoneWorkerRow({ worker, project, highlighted = false }: { worker: PhoneWorker; project: string; highlighted?: boolean }) {
	const { t } = useTranslation();
	const inReview = worker.status === "review_pending";
	return (
		<div className={cn("flex flex-col gap-1 rounded-[14px] border px-3 py-3", highlighted ? LINE : "border-transparent")}>
			<div className={cn("flex items-center gap-2 text-[13px]", MUTED)}>
				<AgentMark agent={worker.provider} className="size-4" />
				<span className="min-w-0 flex-1 truncate">{project}</span>
				{inReview ? (
					<span className="flex shrink-0 items-center gap-1">
						<GitPullRequest className="size-3.5" {...ICON} />
						{t("firstRun.phoneInReview")}
					</span>
				) : (
					<span className={cn("shrink-0 tabular-nums", PASSIVE)}>{worker.minutesAgo}m</span>
				)}
			</div>
			<p className={cn("truncate text-[16px] font-medium", TEXT)}>{worker.title}</p>
			<p className={cn("truncate text-[13px]", worker.pr ? TEXT : PASSIVE)}>
				{worker.pr ? `${t("pr.short")} #${worker.pr} ${t("firstRun.phonePrOpen")}` : worker.branch}
			</p>
		</div>
	);
}

// ── Account: AO Cloud and the phone ─────────────────────────────────────────

/**
 * The account step's story, in phases that loop while the phone is in. First
 * AO Cloud: the desktop hands tasks to it down a pipeline, each task a few
 * blocks, and each comes up out of the cloud cut into the platform as a
 * session that keeps running there. Then the phone, a remote for this computer's agents (it talks
 * to the computer directly): the worker waiting on you asks its question, the
 * question reaches the phone as a notification, your answer goes back, and the
 * worker carries on, its card back in Working. Then the cloud again.
 */
type SyncPhase = "cloud" | "waiting" | "asking" | "asked" | "replying" | "answering" | "resumed";
const SYNC_PHASES: ReadonlyArray<readonly [SyncPhase, number]> = [
	["cloud", 9200],
	["waiting", 1300],
	["asking", 1700],
	["asked", 1400],
	["replying", 1300],
	["answering", 1700],
	["resumed", 2600],
];
/** Where the story rests without motion: the question waiting on the phone. */
const SYNC_STILL: SyncPhase = "asked";
type SyncStoryState = { phase: SyncPhase; loop: number };

/** The phone's screen reads the story's phase to show its notification. */
const SyncPhaseContext = createContext<SyncPhase>("cloud");

function useSyncStory(shown: boolean, delay: number): SyncStoryState {
	const reduceMotion = useReducedMotion();
	const [step, setStep] = useState(0);
	useEffect(() => {
		if (!shown || reduceMotion) return;
		let timer = 0;
		let current = 0;
		const schedule = (wait: number) => {
			timer = window.setTimeout(() => {
				current += 1;
				setStep(current);
				schedule(SYNC_PHASES[current % SYNC_PHASES.length]![1]);
			}, wait);
		};
		schedule(delay * 1000 + SYNC_PHASES[0]![1]);
		return () => {
			window.clearTimeout(timer);
			setStep(0);
		};
	}, [shown, reduceMotion, delay]);
	if (!shown) return { phase: "cloud", loop: 0 };
	if (reduceMotion) return { phase: SYNC_STILL, loop: 0 };
	return { phase: SYNC_PHASES[step % SYNC_PHASES.length]![0], loop: Math.floor(step / SYNC_PHASES.length) };
}

/** The worker that was waiting on you, once your answer reaches it: working again. */
function resumeWaiting(card: PreviewCard): PreviewCard {
	return card.status === "needs_input" ? { ...card, status: "working", displayStatus: undefined, working: true, minutesAgo: 0 } : card;
}

// Lucide's cloud, cut into AO Cloud's platform.
const CLOUD_PATH = "M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z";
/** AO Cloud's platform lies on the ground behind the window, far enough back
 * for the pipeline between them to read. */
const PLATFORM: Rect = { x: -340, y: -560, width: 860, height: 230 };
const PLATFORM_DEPTH = 12;
/** The cloud cut into the platform's middle, and how deep the cut looks, in
 * the mark's 24-unit box. */
const HOLE_SIZE = 170;
const HOLE = { x: PLATFORM.width / 2 - HOLE_SIZE / 2, y: PLATFORM.height / 2 - HOLE_SIZE / 2 - 10 };
const HOLE_DEPTH = 1.6;

/** AO Cloud's sessions: landscape plates in a row along the platform. */
const SANDBOX_WIDTH = 250;
const SANDBOX_HEIGHT = 150;
const SANDBOX_GAP = 25;
const SANDBOX_DEPTH = 8;
const sandboxSpot = (index: number) => ({ x: 30 + index * (SANDBOX_WIDTH + SANDBOX_GAP), y: 24 });
/** Where a session comes up: centred on the cloud, from just below the face. */
const SANDBOX_RISE = { x: HOLE.x + HOLE_SIZE / 2 - SANDBOX_WIDTH / 2, y: HOLE.y + HOLE_SIZE / 2 - SANDBOX_HEIGHT / 2 };
const SANDBOX_SINK = 20;
/** The order they come up in: the far one, the near one, then the middle one
 * that stays over the cloud, so none rises under one already out. */
const SANDBOX_TURN = [1, 2, 0] as const;

/** The pipeline: a glass duct on the ground from the window's back edge to the
 * platform's near edge, in line with the cloud. */
const PIPE_WIDTH = 30;
const PIPE_HEIGHT = 10;
const PIPE_X = PLATFORM.x + HOLE.x + HOLE_SIZE / 2 - PIPE_WIDTH / 2;
const PIPE_FROM = 0;
const PIPE_TO = PLATFORM.y + PLATFORM.height;
/** Where the phone's pipeline joins this one: a junction box on the line,
 * between the window and the platform. */
const HUB_Y = -165;
const HUB_SIZE = 46;
const HUB_DEPTH = 16;
/** Each task goes down a pipeline as a short train of blocks. */
const BLOCK_SIZE = 18;
const BLOCK_DEPTH = 8;
const BLOCKS_PER_TASK = 3;
const BLOCK_SPACING_S = 0.24;
/** Blocks ride every pipeline at one speed, in plane units a second. */
const BLOCK_SPEED = 210;
const SANDBOXES = [
	{
		id: "sandbox-7e3h",
		title: "firstRun.cloudTask1",
		file: "metering.go",
		added: 21,
		removed: 6,
		diff: [
			["+", "func Meter(ws string) {"],
			["+", "  usage.Add(ws, 1)"],
			["-", "// TODO: meter calls"],
		],
	},
	{
		id: "sandbox-2c91",
		title: "firstRun.cloudTask2",
		file: "UsageChart.tsx",
		added: 36,
		removed: 2,
		diff: [
			["-", "return <Empty />"],
			["+", "return <Chart data={usage} />"],
			["+", "series.map(toBar)"],
		],
	},
	{
		id: "sandbox-b4de",
		title: "firstRun.cloudTask3",
		file: "checkout.spec.ts",
		added: 5,
		removed: 1,
		diff: [
			["-", "await page.waitFor(2000)"],
			["+", "await expect(pay).toBeVisible()"],
			["+", "retries: 0"],
		],
	},
] as const;

/** The phone's label sits past its top end, at the height of its screen. */
const PHONE_LABEL_Y = WINDOW_HEIGHT - PHONE_HEIGHT - 30;

/** The platform with its sessions, and the pipeline to it, on the sheet, for
 * framing the cloud. */
function cloudPoints(): Point[] {
	const { x, y, width, height } = PLATFORM;
	const top = PLATFORM_DEPTH + SANDBOX_DEPTH;
	return [
		toSheet(x, y, top),
		toSheet(x + width, y, top),
		toSheet(x, y + height),
		toSheet(x + width, y + height),
		toSheet(PIPE_X, PIPE_FROM),
		toSheet(PIPE_X + PIPE_WIDTH, PIPE_FROM, PIPE_HEIGHT),
		toSheet(BRANCH_X - PIPE_WIDTH / 2, PHONE_TOP),
		toSheet(BRANCH_X - PIPE_WIDTH / 2, HUB_Y - PIPE_WIDTH / 2),
	];
}

/** The phone, its label and the cable round to the window on the sheet, for
 * framing the phone, with room under them clear of the bottom bar. */
function phonePoints(): Point[] {
	const [left, bottom] = toSheet(CABLE_PHONE_X - CABLE_WIDTH / 2, CABLE_RUN_Y + CABLE_WIDTH / 2);
	return [
		toSheet(PHONE_X, PHONE_LABEL_Y, PHONE_DEPTH),
		toSheet(PHONE_X + PHONE_WIDTH, PHONE_LABEL_Y, PHONE_DEPTH),
		toSheet(PHONE_X, WINDOW_HEIGHT),
		toSheet(PHONE_X + PHONE_WIDTH, WINDOW_HEIGHT),
		toSheet(CABLE_WINDOW_X + CABLE_WIDTH / 2, CABLE_RUN_Y + CABLE_WIDTH / 2),
		[left, bottom + 80],
	];
}

// The pipelines are glass ducts on the ground: a solid floor, the blocks
// riding it, and the glass over them.
type Duct = { rect: Rect; along: "x" | "y"; joints: number[] };
const DUCT_FLOOR = 2;
type PathPoint = { x: number; y: number };

/** The pipeline to AO Cloud, and the line its blocks ride, out from under
 * the window to the platform's side. */
const PIPE: Duct = {
	rect: { x: PIPE_X, y: PIPE_TO, width: PIPE_WIDTH, height: PIPE_FROM - PIPE_TO },
	along: "y",
	// Clear of the junction box.
	joints: [PIPE_TO + 40, PIPE_TO + 100, PIPE_FROM - 100, PIPE_FROM - 40],
};
const PIPE_PATH: PathPoint[] = [
	{ x: PIPE_X + PIPE_WIDTH / 2, y: PIPE_FROM - BLOCK_SIZE / 2 },
	{ x: PIPE_X + PIPE_WIDTH / 2, y: PIPE_TO + BLOCK_SIZE / 2 },
];

/** The phone's pipeline to AO Cloud: out of the phone's top end (under the
 * phone, which hides where it starts), back along the phone's line, then
 * across into the junction box on the desktop's; from there the two share
 * the way to the cloud. */
const PHONE_TOP = WINDOW_HEIGHT - PHONE_HEIGHT;
const BRANCH_X = PHONE_X + PHONE_WIDTH / 2;
const BRANCH_DUCTS: Duct[] = [
	{
		rect: { x: BRANCH_X - PIPE_WIDTH / 2, y: HUB_Y - PIPE_WIDTH / 2, width: PIPE_WIDTH, height: PHONE_TOP - HUB_Y + PIPE_WIDTH / 2 },
		along: "y",
		joints: [PHONE_TOP - 50, (PHONE_TOP + HUB_Y) / 2 - 20],
	},
	{
		rect: { x: BRANCH_X - PIPE_WIDTH / 2, y: HUB_Y - PIPE_WIDTH / 2, width: PIPE_X - BRANCH_X + PIPE_WIDTH / 2, height: PIPE_WIDTH },
		along: "x",
		joints: [0.4, 0.75].map((at) => BRANCH_X + (PIPE_X - BRANCH_X) * at),
	},
];
const BRANCH_PATH: PathPoint[] = [
	{ x: BRANCH_X, y: PHONE_TOP - BLOCK_SIZE / 2 },
	{ x: BRANCH_X, y: HUB_Y },
	{ x: PIPE_X + PIPE_WIDTH / 2, y: HUB_Y },
	{ x: PIPE_X + PIPE_WIDTH / 2, y: PIPE_TO + BLOCK_SIZE / 2 },
];

const pathLength = (path: readonly PathPoint[]) =>
	path.slice(1).reduce((sum, point, index) => sum + Math.hypot(point.x - path[index]!.x, point.y - path[index]!.y), 0);

/** The tasks sent to AO Cloud, in the order their sessions come up: one from
 * the desktop, one from the phone, another from the desktop; seconds after
 * the first leaves. The phone's starts early for its longer way, and the last
 * waits until the phone's are past the junction, so the shared stretch never
 * has two trains on it. */
const CLOUD_SENDS = [
	{ path: PIPE_PATH, at: 0 },
	{ path: BRANCH_PATH, at: 0.3 },
	{ path: PIPE_PATH, at: 4 },
] as const;
/** When a send's last block reaches the cloud. */
const sendArrival = (send: (typeof CLOUD_SENDS)[number]) =>
	send.at + (BLOCKS_PER_TASK - 1) * BLOCK_SPACING_S + pathLength(send.path) / BLOCK_SPEED;

/** The cable between the laptop and the phone, the same kind of duct: out of
 * the window's near side, round in front, and into the phone's port on its
 * bottom end. The question goes down it to the phone as blocks, and your
 * answer comes back up it. */
const CABLE_WIDTH = 22;
const CABLE_HEIGHT = 10;
const CABLE_BLOCK = 14;
const CABLE_BLOCK_DEPTH = 7;
const CABLE_BLOCKS = 3;
const CABLE_TRAVEL_S = 1.2;
const CABLE_WINDOW_X = 40;
const CABLE_PHONE_X = PHONE_X + PHONE_WIDTH / 2;
const CABLE_RUN_Y = WINDOW_HEIGHT + 36;
const CABLE_PATH: PathPoint[] = [
	{ x: CABLE_WINDOW_X, y: WINDOW_HEIGHT + 2 },
	{ x: CABLE_WINDOW_X, y: CABLE_RUN_Y },
	{ x: CABLE_PHONE_X, y: CABLE_RUN_Y },
	{ x: CABLE_PHONE_X, y: WINDOW_HEIGHT + 2 },
];
const CABLE_DUCTS: Duct[] = [
	{
		rect: { x: CABLE_WINDOW_X - CABLE_WIDTH / 2, y: WINDOW_HEIGHT, width: CABLE_WIDTH, height: CABLE_RUN_Y - WINDOW_HEIGHT + CABLE_WIDTH / 2 },
		along: "y",
		joints: [WINDOW_HEIGHT + 6],
	},
	{
		rect: {
			x: CABLE_PHONE_X - CABLE_WIDTH / 2,
			y: CABLE_RUN_Y - CABLE_WIDTH / 2,
			width: CABLE_WINDOW_X - CABLE_PHONE_X + CABLE_WIDTH,
			height: CABLE_WIDTH,
		},
		along: "x",
		joints: [1, 2].map((joint) => CABLE_PHONE_X + ((CABLE_WINDOW_X - CABLE_PHONE_X) * joint) / 3),
	},
	{
		// Its collar at the phone's port.
		rect: { x: CABLE_PHONE_X - CABLE_WIDTH / 2, y: WINDOW_HEIGHT, width: CABLE_WIDTH, height: CABLE_RUN_Y - WINDOW_HEIGHT + CABLE_WIDTH / 2 },
		along: "y",
		joints: [WINDOW_HEIGHT + 6],
	},
];

/**
 * AO Cloud: a platform on the ground with the cloud cut into it (its far wall
 * and its floor seen through the opening) and its name beside the cut, its
 * sessions on top, and what it is for along its near edge.
 */
function CloudPlatform({ children }: { children?: ReactNode }) {
	const { t } = useTranslation();
	const clip = `cloud-cut-${useId().replace(/\W/g, "")}`;
	const unit = HOLE_SIZE / 24;
	return (
		<Slab
			depth={PLATFORM_DEPTH}
			radius={22}
			outline
			style={{ left: PLATFORM.x, top: PLATFORM.y, width: PLATFORM.width, height: PLATFORM.height }}
		>
			<svg
				aria-hidden="true"
				viewBox="0 0 24 24"
				className="absolute overflow-visible"
				style={{ left: HOLE.x, top: HOLE.y, width: HOLE_SIZE, height: HOLE_SIZE }}
			>
				<defs>
					<clipPath id={clip}>
						<path d={CLOUD_PATH} />
					</clipPath>
				</defs>
				<path d={CLOUD_PATH} fill="var(--schematic-wall)" />
				<path
					d={CLOUD_PATH}
					clipPath={`url(#${clip})`}
					transform={`translate(${-HOLE_DEPTH} ${HOLE_DEPTH})`}
					fill="var(--schematic-floor)"
					stroke="var(--schematic-line)"
					strokeWidth={1}
					vectorEffect="non-scaling-stroke"
				/>
				<path d={CLOUD_PATH} fill="none" stroke="var(--schematic-line-strong)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
			</svg>
			{/* Where the pipeline runs on under the face to the cloud. */}
			<span
				aria-hidden="true"
				className={cn("absolute border-x", LINE)}
				style={{ left: PIPE_X - PLATFORM.x, top: HOLE.y + 19 * unit - 4, width: PIPE_WIDTH, bottom: 0 }}
			/>
			{/* Its name runs along the cut's near side. */}
			<span
				className={cn("absolute origin-top-left rotate-90 whitespace-nowrap text-[11px] lowercase tracking-[0.3em]", MUTED)}
				style={{ left: HOLE.x + 2 * unit - 8, top: HOLE.y + 7 * unit }}
			>
				{t("remote.aoCloud")}
			</span>
			{children}
			<span
				className={cn("absolute whitespace-nowrap text-[10px] lowercase tracking-[0.35em]", PASSIVE)}
				style={{ left: PIPE_X - PLATFORM.x + PIPE_WIDTH + 90, bottom: 16 }}
			>
				{t("firstRun.cloudKeepsRunning")}
			</span>
			{/* A grip of three short hatches at the near edge's far end. */}
			{[0, 1, 2].map((hatch) => (
				<span
					key={hatch}
					aria-hidden="true"
					className={cn("absolute h-0 w-2.5 origin-left -rotate-45 border-t", LINE)}
					style={{ right: 30 - hatch * 5, bottom: 16 }}
				/>
			))}
		</Slab>
	);
}

/** AO Cloud's sessions, each rising out of the cloud as its task's last block
 * reaches it, then sliding to its spot; a new loop brings them up again.
 * `start` is when the first task leaves the desktop. */
function SandboxSessions({ loop, start }: { loop: number; start: number }) {
	const reduceMotion = useReducedMotion();
	const below = { x: SANDBOX_RISE.x - SANDBOX_SINK, y: SANDBOX_RISE.y + SANDBOX_SINK };
	// Painted far to near, so the nearer one covers the one behind it.
	const order = SANDBOXES.map((sandbox, index) => ({ sandbox, index })).reverse();
	return (
		<AnimatePresence>
			{order.map(({ sandbox, index }) => {
				const spot = sandboxSpot(index);
				return (
					<motion.div
						key={`${loop}:${sandbox.id}`}
						className="absolute left-0 top-0 will-change-transform"
						style={{ width: SANDBOX_WIDTH, height: SANDBOX_HEIGHT }}
						initial={reduceMotion ? false : { ...below, opacity: 0 }}
						animate={
							reduceMotion
								? spot
								: { x: [below.x, SANDBOX_RISE.x, spot.x], y: [below.y, SANDBOX_RISE.y, spot.y], opacity: [0, 1, 1] }
						}
						exit={{ opacity: 0, transition: { duration: 0.35, ease: EASE_IN_OUT } }}
						transition={{
							duration: 1.3,
							ease: [...EASE_IN_OUT],
							times: [0, 0.4, 1],
							delay: start + sendArrival(CLOUD_SENDS[SANDBOX_TURN[index]]),
						}}
					>
						<SandboxCard sandbox={sandbox} />
					</motion.div>
				);
			})}
		</AnimatePresence>
	);
}

/** A session on AO Cloud, as the same detailed card the board uses: its
 * sandbox, the task, the file the agent is in, its change so far, and where it
 * runs; with two ports on its near edge. */
function SandboxCard({ sandbox }: { sandbox: (typeof SANDBOXES)[number] }) {
	const { t } = useTranslation();
	return (
		<>
			<DetailCard
				depth={SANDBOX_DEPTH}
				radius={14}
				header={
					<>
						<Cloud className="size-2.5" {...ICON} />
						<span className="min-w-0 flex-1 truncate">{sandbox.id}</span>
						<span className="size-1.5 shrink-0 rounded-full bg-(--schematic-add)" />
					</>
				}
				title={t(sandbox.title)}
				activity={<EditingLine file={sandbox.file} />}
				file={sandbox.file}
				diff={sandbox.diff}
				added={sandbox.added}
				removed={sandbox.removed}
				trailing={t("remote.aoCloud")}
			/>
			{[0.74, 0.78].map((at) => (
				<span
					key={at}
					aria-hidden="true"
					className="absolute size-[3px] rounded-full bg-(--schematic-line-strong)"
					style={{ left: SANDBOX_WIDTH * at + SANDBOX_DEPTH / 2, top: SANDBOX_HEIGHT - SANDBOX_DEPTH / 2 - 1.5 }}
				/>
			))}
		</>
	);
}

/** A duct's floor: a thin solid strip on the ground. */
function DuctFloor({ duct }: { duct: Duct }) {
	const { x, y, width, height } = duct.rect;
	return <Slab depth={DUCT_FLOOR} radius={3} solid outline style={{ left: x, top: y, width, height }} />;
}

/** A duct's glass, `lift` up: its top's outline, its upright edges, and a ring
 * at each joint, across the top and down the near side. */
function DuctGlass({ duct, lift }: { duct: Duct; lift: number }) {
	const { x, y, width, height } = duct.rect;
	const upright = (left: number, top: number) => (
		<span
			className={cn("absolute h-0 origin-top-left -rotate-45 border-t", LINE)}
			style={{ left, top, width: lift * Math.SQRT2 }}
		/>
	);
	return (
		<span aria-hidden="true">
			<span className={cn("absolute rounded-[3px] border", LINE)} style={{ left: x + lift, top: y - lift, width, height }} />
			{upright(x, y)}
			{upright(x, y + height)}
			{upright(x + width, y + height)}
			{duct.joints.map((at) =>
				duct.along === "y" ? (
					<span key={at}>
						<span className={cn("absolute h-0 border-t", LINE)} style={{ left: x + lift, top: at - lift, width }} />
						{upright(x, at)}
					</span>
				) : (
					<span key={at}>
						<span className={cn("absolute w-0 border-l", LINE)} style={{ left: at + lift, top: y - lift, height }} />
						{upright(at, y + height)}
					</span>
				),
			)}
		</span>
	);
}

/** A train of `count` blocks riding a duct's floor along `path`, one after
 * another from `start`, each sliding in at one end and out at the other. */
function BlockTrain({
	id,
	path,
	count,
	start,
	travel,
	size,
	depth,
}: {
	id: string;
	path: readonly PathPoint[];
	count: number;
	start: number;
	travel: number;
	size: number;
	depth: number;
}) {
	const lengths = path.slice(1).map((point, index) => Math.hypot(point.x - path[index]!.x, point.y - path[index]!.y));
	const total = lengths.reduce((sum, length) => sum + length, 0);
	// Constant speed: each corner is reached in proportion to the way there.
	const times = lengths.reduce<number[]>((at, length) => [...at, at[at.length - 1]! + length / total], [0]);
	const x = path.map((point) => point.x - size / 2 + DUCT_FLOOR);
	const y = path.map((point) => point.y - size / 2 - DUCT_FLOOR);
	return (
		<>
			{Array.from({ length: count }, (_, block) => {
				const delay = start + block * BLOCK_SPACING_S;
				const along = { duration: travel, ease: "linear" as const, times, delay };
				return (
					<motion.div
						key={`${id}:${block}`}
						className="absolute left-0 top-0 will-change-transform"
						style={{ width: size, height: size }}
						initial={{ x: x[0], y: y[0], opacity: 0 }}
						animate={{ x, y, opacity: [0, 1, 1, 0] }}
						transition={{ x: along, y: along, opacity: { duration: travel, times: [0, 0.1, 0.88, 1], delay } }}
					>
						<Slab depth={depth} radius={2.5} solid outline className="inset-0" />
					</motion.div>
				);
			})}
		</>
	);
}

/** The pipelines to AO Cloud, the desktop's and the phone's merging at a
 * junction box, each task going down as a short train of blocks. */
function Pipeline({ loop, start }: { loop: number; start: number }) {
	const reduceMotion = useReducedMotion();
	return (
		<>
			{[PIPE, ...BRANCH_DUCTS].map((duct, index) => (
				<DuctFloor key={index} duct={duct} />
			))}
			{reduceMotion
				? null
				: CLOUD_SENDS.map((send, turn) => (
						<BlockTrain
							key={`${loop}:${turn}`}
							id={`${loop}:${turn}`}
							path={send.path}
							count={BLOCKS_PER_TASK}
							start={start + send.at}
							travel={pathLength(send.path) / BLOCK_SPEED}
							size={BLOCK_SIZE}
							depth={BLOCK_DEPTH}
						/>
					))}
			{[PIPE, ...BRANCH_DUCTS].map((duct, index) => (
				<DuctGlass key={index} duct={duct} lift={PIPE_HEIGHT} />
			))}
			{/* The junction box over the join: blocks from either side pass through it. */}
			<Slab
				depth={HUB_DEPTH}
				radius={6}
				solid
				outline
				style={{ left: PIPE_X + PIPE_WIDTH / 2 - HUB_SIZE / 2, top: HUB_Y - HUB_SIZE / 2, width: HUB_SIZE, height: HUB_SIZE }}
			/>
		</>
	);
}

/** The cable between the laptop and the phone: the question going down it
 * to the phone, then your answer coming back. */
function Cable({ sync }: { sync: SyncStoryState }) {
	const reduceMotion = useReducedMotion();
	const toPhone = sync.phase === "asking";
	const toLaptop = sync.phase === "answering";
	return (
		<>
			{CABLE_DUCTS.map((duct, index) => (
				<DuctFloor key={index} duct={duct} />
			))}
			{!reduceMotion && (toPhone || toLaptop) ? (
				<BlockTrain
					key={`${sync.phase}:${sync.loop}`}
					id={`${sync.phase}:${sync.loop}`}
					path={toPhone ? CABLE_PATH : [...CABLE_PATH].reverse()}
					count={CABLE_BLOCKS}
					start={0}
					travel={CABLE_TRAVEL_S}
					size={CABLE_BLOCK}
					depth={CABLE_BLOCK_DEPTH}
				/>
			) : null}
			{CABLE_DUCTS.map((duct, index) => (
				<DuctGlass key={index} duct={duct} lift={CABLE_HEIGHT} />
			))}
		</>
	);
}

/** The account step's parts fade in together once the phone is in. */
function StoryLayer({ shown, delay, children }: { shown: boolean; delay: number; children: ReactNode }) {
	const reduceMotion = useReducedMotion();
	return (
		<motion.div
			className="pointer-events-none absolute inset-0"
			initial={false}
			animate={{ opacity: shown ? 1 : 0 }}
			transition={reduceMotion ? { duration: 0 } : { ...PAGE_FADE, delay: shown ? delay : 0 }}
		>
			{children}
		</motion.div>
	);
}

/**
 * The account step's drawing on the ground, under the window and the phone so
 * they hide the pipelines' ends: AO Cloud's platform with its sessions, the
 * pipelines to it, and the phone's label.
 */
function AccountGround({ shown, delay, sync }: { shown: boolean; delay: number; sync: SyncStoryState }) {
	const { t } = useTranslation();
	// The first loop waits for the platform to fade in.
	const start = sync.loop === 0 ? delay + 0.6 : 0.9;
	return (
		<StoryLayer shown={shown} delay={delay}>
			<CloudPlatform>{shown ? <SandboxSessions loop={sync.loop} start={start} /> : null}</CloudPlatform>
			{shown ? <Pipeline loop={sync.loop} start={start} /> : null}
			<span
				className={cn("absolute whitespace-nowrap text-[12px]", TEXT)}
				style={{ left: PHONE_X + PHONE_DEPTH - 8, top: PHONE_LABEL_Y - PHONE_DEPTH }}
			>
				{t("firstRun.signInPerkMobile")}
			</span>
		</StoryLayer>
	);
}

/** The cable to the phone, in front of the window and the phone, over both. */
function AccountFront({ shown, delay, sync }: { shown: boolean; delay: number; sync: SyncStoryState }) {
	return (
		<StoryLayer shown={shown} delay={delay}>
			<Cable sync={sync} />
		</StoryLayer>
	);
}
