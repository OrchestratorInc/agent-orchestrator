import { Bell, CircleCheckBig, ListFilter, Loader, Menu, MessageSquare, Plus, type LucideIcon } from "lucide-react";
import { useId, useMemo, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { statusColor, themeFor, type MobileTheme } from "../../lib/mobile-theme";
import { useUiStore } from "../../stores/ui-store";
import { AgentAvatar } from "../AgentAvatar";

// The mobile app's Workers tab (packages/mobile/app/(tabs)/index.tsx) as it
// draws on an iPhone, at the phone's logical size (393pt wide). Every metric is
// the mobile source's: ScreenHeader and ListSectionHeader (lib/ui.tsx), the
// row (lib/worker-list-row.tsx), the iOS dock (lib/worker-dock.ios.tsx,
// lib/worker-dock-layout.ts), the type scale and spacing (lib/tokens.ts). The
// palette is the phone's own derivation of the desktop's selected theme
// (lib/theme.ts themeFor, mirrored in lib/mobile-theme.ts), so both devices show the same theme.

export const PHONE_SCREEN_WIDTH = 393;
/** iPhone safe-area insets with the Dynamic Island. */
const SAFE_TOP = 59;
const SAFE_BOTTOM = 34;

const SANS = "var(--font-family-base)";
const MONO = "var(--font-family-mono)";
/** iOS draws the status bar in SF Pro, not the app's font. */
const SYSTEM = "-apple-system, BlinkMacSystemFont, system-ui, sans-serif";

/** tokens.ts `space`. */
const space = { hair: 2, xxs: 4, xs: 6, sm: 8, md: 12, lg: 16 } as const;

export type PhoneWorkerStatus = "working" | "needs_input" | "mergeable";

export type PhoneWorker = {
	id: string;
	provider: string;
	title: string;
	branch: string;
	status: PhoneWorkerStatus;
	minutesAgo: number;
	prNumber?: number;
};

/** agentsView.ts workerStatusGlyph, as lucide's matches for the Feather names. */
const STATUS_GLYPH: Record<PhoneWorkerStatus, LucideIcon> = {
	working: Loader,
	needs_input: MessageSquare,
	mergeable: CircleCheckBig,
};

function usePhoneTheme(): { theme: MobileTheme; dark: boolean } {
	const style = useUiStore((state) => state.themeStyle);
	const scheme = useUiStore((state) => state.resolvedTheme);
	return useMemo(() => ({ theme: themeFor(scheme, style), dark: scheme === "dark" }), [scheme, style]);
}

/**
 * The displacement map for a round lens: red carries x, green carries y, and
 * the blurred neutral (128,128) disc leaves the middle undisplaced, so only the
 * rim bends what is behind it — the way a glass dome refracts at its edge.
 */
function lensMap(size: number): string {
	const edge = size * 0.16;
	const svg = `<svg xmlns='http://www.w3.org/2000/svg' width='${size}' height='${size}' viewBox='0 0 ${size} ${size}'><defs><linearGradient id='r' x1='0' x2='1' y1='0' y2='0'><stop offset='0' stop-color='#000'/><stop offset='1' stop-color='#f00'/></linearGradient><linearGradient id='g' x1='0' x2='0' y1='0' y2='1'><stop offset='0' stop-color='#000'/><stop offset='1' stop-color='#0f0'/></linearGradient><filter id='b' x='-50%' y='-50%' width='200%' height='200%'><feGaussianBlur stdDeviation='${edge / 2}'/></filter></defs><rect width='${size}' height='${size}' fill='url(#r)'/><rect width='${size}' height='${size}' fill='url(#g)' style='mix-blend-mode:screen'/><circle cx='${size / 2}' cy='${size / 2}' r='${size / 2 - edge}' fill='rgb(128,128,0)' filter='url(#b)'/></svg>`;
	return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

/**
 * A round iOS Liquid Glass surface (glass.ts glassCircle) for Chromium: the
 * backdrop is refracted through an SVG displacement lens, lightly blurred and
 * saturated, under a faint tint, a specular rim lit from the top left and a
 * soft shadow below.
 */
export function LiquidGlass({ size, dark, children }: { size: number; dark: boolean; children?: ReactNode }) {
	const id = `liquid-glass-${useId().replace(/[^a-zA-Z0-9-]/g, "")}`;
	const map = useMemo(() => lensMap(size), [size]);
	const rim: CSSProperties = {
		padding: 1,
		background: dark
			? "linear-gradient(135deg, rgba(255,255,255,0.5), rgba(255,255,255,0.1) 32%, rgba(255,255,255,0.03) 62%, rgba(255,255,255,0.22))"
			: "linear-gradient(135deg, rgba(255,255,255,0.95), rgba(255,255,255,0.4) 35%, rgba(0,0,0,0.06) 65%, rgba(255,255,255,0.7))",
		mask: "linear-gradient(#000 0 0) content-box exclude, linear-gradient(#000 0 0)",
	};
	return (
		<span
			className="relative flex shrink-0 items-center justify-center rounded-full"
			style={{
				width: size,
				height: size,
				backdropFilter: `url(#${id}) blur(1.5px) saturate(1.6) brightness(${dark ? 1.08 : 1.02})`,
				background: dark ? "rgba(255,255,255,0.055)" : "rgba(255,255,255,0.55)",
				boxShadow: dark
					? "inset 0 1px 1px rgba(255,255,255,0.12), inset 0 -1px 2px rgba(0,0,0,0.3), 0 4px 14px rgba(0,0,0,0.35)"
					: "inset 0 1px 1px rgba(255,255,255,0.9), inset 0 -1px 2px rgba(0,0,0,0.05), 0 4px 14px rgba(0,0,0,0.12)",
			}}
		>
			<svg aria-hidden="true" width="0" height="0" className="absolute">
				<filter id={id} x="0" y="0" width={size} height={size} filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
					<feImage href={map} x="0" y="0" width={size} height={size} result="lens" />
					<feDisplacementMap in="SourceGraphic" in2="lens" scale={size * 0.24} xChannelSelector="R" yChannelSelector="G" />
				</filter>
			</svg>
			<span className="pointer-events-none absolute inset-0 rounded-full" style={rim} />
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

/** worker-list-row.tsx WorkerRowContents: eyebrow, title, details. */
function WorkerRow({ worker, project, theme }: { worker: PhoneWorker; project: string; theme: MobileTheme }) {
	const { t } = useTranslation();
	const color = statusColor(theme, worker.status);
	const Glyph = STATUS_GLYPH[worker.status];
	const label =
		worker.status === "working"
			? t("displayStatus.working")
			: worker.status === "needs_input"
				? t("firstRun.phoneNeedsInput")
				: t("displayStatus.mergeable");
	// prLine: an open PR reads "PR #42 open" in the success tone; without one
	// the row names its branch.
	const details = worker.prNumber ? t("firstRun.phonePrOpen", { number: worker.prNumber }) : worker.branch;
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
				<Glyph
					aria-hidden="true"
					className={worker.status === "working" ? "motion-safe:animate-spin" : undefined}
					size={12}
					strokeWidth={2}
					color={color}
				/>
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
			<span
				className="truncate"
				style={{ fontFamily: MONO, fontSize: 12, lineHeight: "16px", color: worker.prNumber ? theme.green : theme.textTertiary }}
			>
				{details}
			</span>
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
