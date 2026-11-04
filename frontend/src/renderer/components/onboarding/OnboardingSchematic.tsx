import {
	ArrowUp,
	Bell,
	CalendarClock,
	Check,
	ChevronDown,
	Code,
	Coffee,
	Cog,
	EllipsisVertical,
	File,
	Folder,
	GitPullRequest,
	Layers,
	LayoutDashboard,
	ListFilter,
	LogIn,
	PanelLeft,
	PanelRight,
	Play,
	Plus,
	Search,
	Settings,
	ShieldCheck,
	Smartphone,
	SquarePen,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion, type TargetAndTransition, type Transition } from "motion/react";
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { GITHUB_MARK_PATH } from "../CreateProjectFlow";
import { OrchestratorIcon } from "../icons";
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
const FACE = "bg-(--schematic-page)";
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
	if (phone) rects.push({ x: -(PHONE_WIDTH + PHONE_GAP), y: WINDOW_HEIGHT - PHONE_HEIGHT, width: PHONE_WIDTH, height: PHONE_HEIGHT });
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
						<circle cx={bx} cy={by} r={BALLOON_R} fill="var(--schematic-page)" stroke="var(--schematic-line-strong)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
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
function volume(depth: number, fill = "var(--schematic-page)"): string {
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
				<svg viewBox="0 0 12 12" className="size-3 motion-safe:animate-spin [animation-duration:1.4s]">
					<circle cx="6" cy="6" r="4.5" fill="none" stroke="var(--schematic-line)" strokeWidth="1" />
					<path d="M6 1.5a4.5 4.5 0 0 1 4.5 4.5" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" />
				</svg>
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

/** An agent's avatar reduced to its outline. */
function AgentMark({ className }: { className?: string }) {
	return <span aria-hidden="true" className={cn("size-3.5 shrink-0 rounded-full border", LINE, className)} />;
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
		...[...callouts.chat, ...callouts.board, ...callouts.new, ...callouts.phone].flatMap(calloutExtent),
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
	// Once the phone is in, its callout takes the second one's place, whose
	// balloon would otherwise sit over the phone.
	const shownCallouts = phoneShown ? [...callouts[page].slice(0, 1), ...callouts.phone] : callouts[page];
	const overview = frameCamera(bounds(overviewPoints(phoneShown, extents)), FIT_MARGIN);
	// Close in on what the story is about, but not so far the app stops reading as the app.
	const focusFrame = focus === "overview" ? null : bounds(focusPoints(focus, pileCards.length));
	const camera = focusFrame ? frameCamera(focusFrame, FOCUS_MARGIN, overview.scale * MAX_FOCUS_ZOOM) : overview;
	return (
		// The edges fall away so the middle, where the camera points, reads first.
		<div className="relative" style={{ width: APP_WIDTH, height: APP_HEIGHT, maskImage: VIGNETTE, WebkitMaskImage: VIGNETTE }}>
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
					<motion.div
						className="absolute"
						style={{ left: -(PHONE_WIDTH + PHONE_GAP), top: WINDOW_HEIGHT - PHONE_HEIGHT, width: PHONE_WIDTH, height: PHONE_HEIGHT }}
						initial={false}
						// Lowered onto the sheet: straight down on screen is (-1, +1) on the plane.
						animate={phoneShown ? { opacity: 1, x: 0, y: 0 } : { opacity: 0, x: PHONE_DROP, y: -PHONE_DROP }}
						transition={transition}
					>
						<Phone>{phone}</Phone>
					</motion.div>
					<Slab depth={WINDOW_DEPTH} radius={18} outline className="inset-0">
						<WindowFace
							page={page}
							project={project}
							cards={cards}
							sidebarCards={sidebarCards}
							boardKey={boardKey}
							runId={runId}
							pileCards={pileCards}
							githubShown={githubShown}
							agents={pickerAgents}
							agent={agent}
						/>
					</Slab>
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
			<Viewfinder frame={focusFrame} camera={camera} transition={cameraMove} />
		</div>
	);
}

// ── Camera ──────────────────────────────────────────────────────────────────

/** What the camera frames at each beat of the story. */
export type SceneFocus = "overview" | "chat" | "working" | "ci" | "new";

const CAMERA_MOVE = { duration: 1.3, ease: [0.65, 0, 0.35, 1] } as const;
const FOCUS_MARGIN = 56;
/** How far past the overview the camera may close in. */
const MAX_FOCUS_ZOOM = 2.1;
const VIGNETTE = "radial-gradient(ellipse 78% 74% at 50% 50%, black 62%, transparent 100%)";

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
function focusPoints(focus: Exclude<SceneFocus, "overview">, pileCount: number): Point[] {
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
	}
}

/** Four corner marks framing what the camera is on, gliding with it. */
function Viewfinder({ frame, camera, transition }: { frame: Frame | null; camera: Camera; transition: Transition }) {
	const pad = 18;
	const box = frame
		? {
				left: camera.x + camera.scale * frame.left - pad,
				top: camera.y + camera.scale * frame.top - pad,
				width: camera.scale * (frame.right - frame.left) + 2 * pad,
				height: camera.scale * (frame.bottom - frame.top) + 2 * pad,
			}
		: { left: 0, top: 0, width: APP_WIDTH, height: APP_HEIGHT };
	const corner = "absolute size-4 border-(--schematic-line-strong)";
	return (
		<motion.div
			aria-hidden="true"
			className="pointer-events-none absolute"
			initial={false}
			animate={{ ...box, opacity: frame ? 1 : 0 }}
			transition={transition}
		>
			<span className={cn(corner, "left-0 top-0 border-l border-t")} />
			<span className={cn(corner, "right-0 top-0 border-r border-t")} />
			<span className={cn(corner, "bottom-0 left-0 border-b border-l")} />
			<span className={cn(corner, "bottom-0 right-0 border-b border-r")} />
		</motion.div>
	);
}

/** Where the drawing's callouts point on each page (on the plane, raised to
 * the face they mark) and where their balloons sit on the sheet: up and to
 * the left of the window's top edge, or down and to the left of its side. */
const PANEL_X = SIDEBAR_WIDTH;
const PANEL_Y = 10;
const PANEL_Z = WINDOW_DEPTH + PANEL_DEPTH;

function useCallouts(): Record<ScenePage | "phone", Callout[]> {
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
		phone: [callout(t("settings.connectMobile"), -(PHONE_WIDTH + PHONE_GAP) + PHONE_WIDTH / 2, WINDOW_HEIGHT - PHONE_HEIGHT + 40, PHONE_DEPTH, -40, -170)],
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
	className,
	style,
}: {
	placeholder: string;
	agent?: { id: string; label: string };
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
					<svg aria-hidden="true" viewBox="0 0 14 14" className="size-3.5">
						<circle cx="7" cy="7" r="5.5" fill="none" stroke="var(--schematic-line)" strokeWidth="1" />
						<path d="M7 1.5a5.5 5.5 0 0 1 5.2 3.7" fill="none" stroke="currentColor" strokeWidth="1" />
					</svg>
					<span className="flex items-center gap-1.5">
						<AgentMark />
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
						<TopbarAction icon={<LayoutDashboard className="size-3" {...ICON} />}>{t("shell.openKanban")}</TopbarAction>
						<Code className="size-3.5" {...ICON} />
						<Play className="size-3.5" {...ICON} />
						<PanelRight className="size-3.5" {...ICON} />
						<Bell className="size-3.5" {...ICON} />
					</>
				}
			>
				{/* The open tab, underlined where it meets the topbar's rule. */}
				<span className={cn("relative flex items-center gap-2 self-stretch pr-1 text-[11.5px]", TEXT)}>
					<AgentMark />
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
				<Composer placeholder={t("sideChat.messageTheAgent")} agent={agent} style={{ left: 28, right: 34, top: 10, bottom: 22 }} />
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
const CARD_HEIGHT = 104;
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
	const [visit, setVisit] = useState<{ pr?: number; at: number } | null>(null);
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
		if (sent) setVisit({ pr: sent.pr, at: Date.now() });
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

/** A check's state `elapsed` seconds into a pull request's visit. */
function checkState(elapsed: number | null, doneAt: number): CheckState {
	if (elapsed === null || elapsed > ROUND_TRIP_S + 0.6) return "idle";
	if (elapsed < ROUND_TRIP_TIMES[1]! * ROUND_TRIP_S) return "idle";
	return elapsed < doneAt ? "running" : "passed";
}

/** GitHub's mark as a line: one outline of its shape, at height `lift`. */
function GitHubOutline({ lift, className }: { lift: number; className?: string }) {
	return (
		<svg
			aria-hidden="true"
			viewBox="0 0 24 24"
			className={cn("absolute overflow-visible", className)}
			style={{ left: GITHUB_X + lift, top: GITHUB_Y - lift, width: GITHUB_SIZE, height: GITHUB_SIZE }}
		>
			<path
				d={GITHUB_MARK_PATH}
				fill="none"
				stroke="currentColor"
				strokeWidth={1}
				vectorEffect="non-scaling-stroke"
			/>
		</svg>
	);
}

/**
 * GitHub beside the window, as an isometric skeleton of its mark: its base
 * and top outlines, hovering. The pull request's CI and code review are
 * labels beside it, and run while a card is on it. Shown only in the story's
 * CI part.
 */
function GitHubSkeleton({ shown, visit }: { shown: boolean; visit: { pr?: number; at: number } | null }) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!visit) return;
		const timer = window.setInterval(() => setNow(Date.now()), 100);
		const stop = window.setTimeout(() => window.clearInterval(timer), (ROUND_TRIP_S + 0.8) * 1000);
		return () => {
			window.clearInterval(timer);
			window.clearTimeout(stop);
		};
	}, [visit]);
	const elapsed = visit ? (now - visit.at) / 1000 : null;
	const ci = checkState(elapsed, CI_DONE_S);
	const review = checkState(elapsed, REVIEW_DONE_S);
	const busy = ci !== "idle";
	const top = GITHUB_FLOAT + GITHUB_DEPTH;
	return (
		<motion.div
			className="absolute inset-0"
			initial={false}
			animate={{ opacity: shown ? 1 : 0 }}
			transition={reduceMotion ? { duration: 0 } : PAGE_FADE}
		>
			{/* The mark extruded: its base and top outlines. */}
			<GitHubOutline lift={GITHUB_FLOAT} className="text-(--schematic-line)" />
			<GitHubOutline lift={top} className={cn("transition-colors duration-300", busy ? "text-(--schematic-text)" : "text-(--schematic-text-muted)")} />
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

function WorkerCard({ card }: { card: PreviewCard }) {
	const laneLabel = useLaneLabel();
	return (
		<Slab depth={RAISED_DEPTH} radius={9} solid className="inset-0">
			<div className="flex size-full flex-col justify-between px-3 py-2.5">
				<div className="flex items-start gap-2">
					<AgentMark className="mt-px size-3" />
					<p className={cn("line-clamp-2 min-w-0 text-[11px] leading-snug", TEXT)}>{card.title}</p>
				</div>
				<div className={cn("flex items-center justify-between gap-2 text-[10px]", PASSIVE)}>
					<span className="truncate">{card.branch}</span>
					{card.pr ? <span className="shrink-0 tabular-nums">#{card.pr}</span> : null}
				</div>
				<div className={cn("flex items-center gap-1.5 text-[10px]", MUTED)}>
					<StatusGlyph status={card.status} />
					<span className="min-w-0 flex-1 truncate">{card.displayStatus ?? laneLabel(laneOf(card.status))}</span>
					<span className={cn("tabular-nums", PASSIVE)}>{card.minutesAgo}m</span>
				</div>
			</div>
		</Slab>
	);
}

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
					<motion.div
						className="absolute left-6 w-[230px]"
						style={{ bottom: 64, height: menuHeight }}
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
										<AgentMark className="size-3" />
										<span className="min-w-0 flex-1 truncate">{candidate.label}</span>
										{candidate.id === agent?.id ? <Check className="size-3" {...ICON} /> : null}
									</div>
								))}
								<div className={cn("mx-1.5 mt-auto flex h-8 items-center border-t px-2.5 text-[10.5px]", LINE, MUTED)}>{t("agentSelector.manage")}</div>
							</div>
						</Slab>
					</motion.div>
					<Composer placeholder={t("firstRun.exampleComposerDraft")} agent={agent} className="inset-0" />
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
	const pinned = workers.filter((worker) => worker.status === "needs_input");
	const recent = workers.filter((worker) => worker.status !== "needs_input");
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
					<PhoneWorkerRow key={worker.id} worker={worker} project={project} highlighted />
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
				<AgentMark className="size-4" />
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
