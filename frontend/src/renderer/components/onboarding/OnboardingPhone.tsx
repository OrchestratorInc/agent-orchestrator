import { Bell, CircleCheckBig, GitPullRequest, ListFilter, Loader, Menu, MessageSquare, Plus, type LucideIcon } from "lucide-react";
import { type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { statusColor, type MobileTheme } from "../../lib/mobile-theme";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";

// The mobile app's Workers tab (packages/mobile/app/(tabs)/index.tsx) as it
// draws on an iPhone, at the phone's logical size (393pt wide). Every metric is
// the mobile source's: ScreenHeader and ListSectionHeader (lib/ui.tsx), the
// row (lib/worker-list-row.tsx), the iOS dock (lib/worker-dock.ios.tsx,
// lib/worker-dock-layout.ts), the type scale and spacing (lib/tokens.ts). The
// palette is the onboarding illustration's line-drawing one (SCHEMATIC_THEME).

export const PHONE_SCREEN_WIDTH = 393;
/** iPhone safe-area insets with the Dynamic Island. */
const SAFE_TOP = 59;
const SAFE_BOTTOM = 34;

const SANS = "var(--font-family-base)";
/** theme.fontMono: Geist Mono, which the desktop also loads. */
const MONO = "var(--font-family-mono)";
/** The schematic labels everything in mono, the status bar included. */
const SYSTEM = MONO;

/** tokens.ts `space`. */
const space = { hair: 2, xxs: 4, xs: 6, sm: 8, md: 12, lg: 16 } as const;

export type PhoneWorkerStatus = "working" | "needs_input" | "mergeable" | "review_pending";

export type PhoneWorker = {
	id: string;
	provider: string;
	title: string;
	status: PhoneWorkerStatus;
	minutesAgo: number;
	branch?: string;
	pr?: number;
};

/** agentsView.ts workerStatusGlyph, as lucide's matches for the Feather names. */
const STATUS_GLYPH: Record<PhoneWorkerStatus, LucideIcon> = {
	working: Loader,
	needs_input: MessageSquare,
	mergeable: CircleCheckBig,
	review_pending: GitPullRequest,
};

/** The onboarding illustration's line-drawing palette (styles.css
 * .onboarding-preview-stage): clear fills, and lines, text and status all in
 * the page's foreground, so it is black and white in either theme. */
const SCHEMATIC_TEXT = "var(--schematic-text)";
const SCHEMATIC_THEME: MobileTheme = {
	bgBase: "transparent",
	bgElevated: "transparent",
	textPrimary: SCHEMATIC_TEXT,
	textSecondary: "var(--schematic-text-muted)",
	textTertiary: "var(--schematic-text-muted)",
	textFaint: "var(--schematic-text-passive)",
	borderSubtle: "var(--schematic-line)",
	borderDefault: "var(--schematic-line-strong)",
	accent: SCHEMATIC_TEXT,
	orange: SCHEMATIC_TEXT,
	amber: SCHEMATIC_TEXT,
	red: SCHEMATIC_TEXT,
	green: SCHEMATIC_TEXT,
	purple: SCHEMATIC_TEXT,
	blue: SCHEMATIC_TEXT,
};

function usePhoneTheme(): { theme: MobileTheme; dark: boolean } {
	return { theme: SCHEMATIC_THEME, dark: true };
}

/** A round header button drawn as a single stroke (the line-drawing stand-in
 * for iOS Liquid Glass). */
export function LiquidGlass({ size, children }: { size: number; dark: boolean; children?: ReactNode }) {
	return (
		<span
			className="relative flex shrink-0 items-center justify-center rounded-full"
			style={{ width: size, height: size, boxShadow: `inset 0 0 0 1px ${SCHEMATIC_THEME.borderSubtle}` }}
		>
			{children}
		</span>
	);
}

/** native-header-button.ios.tsx: a 44pt glass circle with a 20pt glyph. */
const GLASS_CIRCLE_SIZE = 44;

function StatusBar({ color }: { color: string }) {
	return (
		<div className="relative" style={{ height: SAFE_TOP, color }}>
			<span
				className="absolute text-center tabular-nums"
				style={{ left: 0, width: 133, top: 18, fontFamily: SYSTEM, fontSize: 17, lineHeight: "22px", fontWeight: 600, letterSpacing: -0.4 }}
			>
				9:41
			</span>
			<span className="absolute rounded-full bg-black" style={{ left: (PHONE_SCREEN_WIDTH - 126) / 2, top: 11, width: 126, height: 37 }} />
			<span className="absolute flex items-center" style={{ right: 26, top: 22, gap: 6 }}>
				<svg width="18" height="12" viewBox="0 0 18 12" fill="currentColor" aria-hidden="true">
					<rect x="0" y="7.5" width="3.2" height="4.5" rx="0.9" />
					<rect x="4.8" y="5.3" width="3.2" height="6.7" rx="0.9" />
					<rect x="9.6" y="2.8" width="3.2" height="9.2" rx="0.9" />
					<rect x="14.4" y="0" width="3.2" height="12" rx="0.9" />
				</svg>
				<svg width="16" height="12" viewBox="0 0 16 12" fill="currentColor" aria-hidden="true">
					<path d="M8 2.6c2.2 0 4.2.85 5.7 2.25l1.15-1.17A9.7 9.7 0 0 0 8 1 9.7 9.7 0 0 0 1.15 3.68L2.3 4.85A8.1 8.1 0 0 1 8 2.6Z" />
					<path d="M8 5.85c1.33 0 2.55.5 3.47 1.33l1.15-1.17A6.6 6.6 0 0 0 8 4.25a6.6 6.6 0 0 0-4.62 1.76l1.15 1.17A5.1 5.1 0 0 1 8 5.85Z" />
					<path d="M8 9.1c.45 0 .87.16 1.2.43L8 10.75 6.8 9.53c.33-.27.75-.43 1.2-.43Z" />
				</svg>
				<svg width="27" height="13" viewBox="0 0 27 13" fill="none" aria-hidden="true">
					<rect x="0.5" y="0.5" width="23" height="12" rx="3.8" stroke="currentColor" strokeOpacity="0.35" />
					<rect x="2" y="2" width="20" height="9" rx="2.5" fill="currentColor" />
					<path d="M25 4.5v4c.8-.3 1.3-1.1 1.3-2s-.5-1.7-1.3-2Z" fill="currentColor" fillOpacity="0.4" />
				</svg>
			</span>
		</div>
	);
}

/** ui.tsx ListSectionHeader: caption1 medium in textTertiary, no count. */
function SectionHeader({ label, theme }: { label: string; theme: MobileTheme }) {
	return (
		<div
			className="flex items-center"
			style={{ gap: space.sm, padding: `${space.lg}px ${space.lg}px ${space.xxs}px`, fontFamily: SANS, fontSize: 12, lineHeight: "16px", fontWeight: 500, color: theme.textTertiary }}
		>
			{label}
		</div>
	);
}

/** worker-list-row.tsx WorkerRowContents: eyebrow, title and the details line
 * (the pull request, else the branch). */
function WorkerRow({ worker, project, theme }: { worker: PhoneWorker; project: string; theme: MobileTheme }) {
	const { t } = useTranslation();
	const color = statusColor(theme, worker.status);
	const Glyph = STATUS_GLYPH[worker.status];
	const label =
		worker.status === "working"
			? t("displayStatus.working")
			: worker.status === "needs_input"
				? t("firstRun.phoneNeedsInput")
				: worker.status === "mergeable"
					? t("displayStatus.mergeable")
					: t("firstRun.phoneInReview");
	// agentsView.ts prLine: "PR #42 open" in the success tone, else the branch.
	const details = worker.pr ? `${t("pr.short")} #${worker.pr} ${t("firstRun.phonePrOpen")}` : worker.branch;
	const detailsColor = worker.pr ? theme.green : theme.textTertiary;
	const caption: CSSProperties = { fontFamily: SANS, fontSize: 12, lineHeight: "16px", fontWeight: 500 };
	return (
		<div
			className="flex flex-col"
			style={{ minHeight: 76, padding: `${space.sm}px ${space.lg}px`, gap: space.hair, borderBottom: `0.5px solid ${theme.borderSubtle}`, background: theme.bgBase }}
		>
			<div className="flex items-center" style={{ gap: space.xs, minHeight: 17 }}>
				<AgentAvatar provider={worker.provider} decorative className="size-[14px]" />
				<span className="min-w-0 flex-1 truncate" style={{ ...caption, color: theme.textSecondary }}>
					{project}
				</span>
				{/* Turned on an HTML wrapper so the spin is composited (see StatusGlyph). */}
				<span aria-hidden="true" className={cn("grid size-3 shrink-0 place-items-center", worker.status === "working" && "will-change-transform motion-safe:animate-spin")}>
					<Glyph size={12} strokeWidth={2} color={color} />
				</span>
				<span className="shrink-0 tabular-nums" style={{ ...caption, color }}>
					{label}
				</span>
			</div>
			<span
				className="truncate"
				style={{ fontFamily: SANS, fontSize: 16, lineHeight: "21px", fontWeight: 600, letterSpacing: -0.15, color: theme.textPrimary }}
			>
				{worker.title}
			</span>
			{details ? (
				<span className="truncate" style={{ fontFamily: MONO, fontSize: 12, lineHeight: "16px", color: detailsColor }}>
					{details}
				</span>
			) : null}
		</div>
	);
}

/**
 * The Workers tab with the given workers: Needs you (oldest first), then
 * Recent (newest first), as agentsView.ts groupSessions orders them.
 */
export function PhoneWorkersScreen({ project, workers }: { project: string; workers: readonly PhoneWorker[] }) {
	const { t } = useTranslation();
	const { theme, dark } = usePhoneTheme();
	const needsYou = workers.filter((worker) => worker.status === "needs_input").sort((a, b) => b.minutesAgo - a.minutesAgo);
	const recent = workers.filter((worker) => worker.status !== "needs_input").sort((a, b) => a.minutesAgo - b.minutesAgo);
	const glyph = { size: 20, strokeWidth: 1.75, color: theme.textSecondary, "aria-hidden": true } as const;
	return (
		<div
			className={`relative size-full overflow-hidden antialiased ${dark ? "dark" : ""}`}
			style={{ background: theme.bgBase, color: theme.textPrimary }}
		>
			<StatusBar color={theme.textPrimary} />
			{/* ScreenHeader: sidebar button, title, notifications. */}
			<div className="flex items-center" style={{ gap: space.md, padding: `${space.sm}px ${space.lg}px` }}>
				<LiquidGlass size={GLASS_CIRCLE_SIZE} dark={dark}>
					<Menu {...glyph} />
				</LiquidGlass>
				<span
					className="min-w-0 flex-1 truncate"
					style={{ fontFamily: SANS, fontSize: 28, lineHeight: "34px", fontWeight: 600, letterSpacing: -0.5, color: theme.textPrimary }}
				>
					{t("inspector.workers")}
				</span>
				<span className="relative">
					<LiquidGlass size={GLASS_CIRCLE_SIZE} dark={dark}>
						<Bell {...glyph} />
					</LiquidGlass>
					{/* A worker is waiting on you, so there is an unread notification. */}
					<span
						className="absolute rounded-full"
						style={{ top: 0, right: 1, width: 9, height: 9, background: theme.accent, border: `1.5px solid ${theme.bgBase}` }}
					/>
				</span>
			</div>
			{needsYou.length > 0 ? <SectionHeader label={t("zone.action")} theme={theme} /> : null}
			{needsYou.map((worker) => <WorkerRow key={worker.id} worker={worker} project={project} theme={theme} />)}
			{recent.length > 0 ? <SectionHeader label={t("firstRun.phoneRecent")} theme={theme} /> : null}
			{recent.map((worker) => <WorkerRow key={worker.id} worker={worker} project={project} theme={theme} />)}
			{/* The iOS dock at rest: filter menu, then spawn, floating over the list. */}
			<div className="absolute flex items-center justify-between" style={{ left: space.lg, right: space.lg, bottom: SAFE_BOTTOM + 12, height: 52 }}>
				<LiquidGlass size={GLASS_CIRCLE_SIZE} dark={dark}>
					<ListFilter size={18} strokeWidth={1.75} color={theme.textSecondary} aria-hidden="true" />
				</LiquidGlass>
				<LiquidGlass size={GLASS_CIRCLE_SIZE} dark={dark}>
					<Plus size={18} strokeWidth={1.75} color={theme.textPrimary} aria-hidden="true" />
				</LiquidGlass>
			</div>
			<span
				className="absolute rounded-full"
				style={{ left: (PHONE_SCREEN_WIDTH - 134) / 2, bottom: 8, width: 134, height: 5, background: theme.textPrimary }}
			/>
		</div>
	);
}
