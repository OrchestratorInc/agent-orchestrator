import { useState } from "react";
import { ArrowUp, RotateCw } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { UpdateStatus } from "../../main/update-settings";
import { parseNightlyVersion } from "../lib/build-channel";
import { requestUpdateDownload } from "../hooks/useUpdateStatus";
import { cn } from "../lib/utils";
import { DesktopReleaseNotes } from "./DesktopReleaseNotes";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { Button } from "./ui/button";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** Collapsed footer icon-rail control: same growing highlight in the square. */
export const FOOTER_RAIL_BUTTON_CLASS = cn(
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"grid size-control-board place-items-center rounded-lg text-muted-foreground [&_svg]:size-icon-base",
);

type FailedPhase = "download" | "install";

/**
 * What the sidebar should act on, derived from the live update status.
 *
 * `status.state` alone is not enough. It cycles through checking → available →
 * not-available on every background check while a staged build sits untouched,
 * which blinked the restart row out of existence every 15 minutes on nightly.
 * `status.staged` is stamped on every status by the main process for exactly
 * this reason, so the staged build is read from there rather than from `state`.
 *
 * Failing update *checks* never reach the sidebar: they say nothing the person
 * can act on, and Settings › Updates already reports them. Only a download or
 * install that actually failed does.
 */
export type SidebarUpdateAction =
	| { kind: "downloading"; percent?: number }
	| { kind: "download"; version?: string; releaseNotes?: string }
	| { kind: "install"; version?: string }
	| { kind: "failed"; phase: FailedPhase }
	| null;

export function sidebarUpdateAction(status: UpdateStatus, failedPhase: FailedPhase | null = null): SidebarUpdateAction {
	if (status.state === "downloading") {
		// Absent until the first progress event of a requested download.
		const percent = status.percent === undefined ? undefined : Math.min(100, Math.max(0, status.percent));
		return { kind: "downloading", percent };
	}
	if (status.state === "error" && failedPhase) return { kind: "failed", phase: failedPhase };
	// `staged` is the stamp the main process puts on every status; the
	// `downloaded` fallback keeps this correct for any status that predates it
	// or arrives from a source that does not stamp.
	const staged =
		status.staged ??
		(status.state === "downloaded" ? { version: status.version } : undefined);
	// Something newer than what is already staged still deserves the download
	// action; the main process reports a re-discovered staged build as
	// "downloaded", so an "available" here is genuinely a different version.
	if (status.state === "available" && status.version !== staged?.version) {
		return { kind: "download", version: status.version, releaseNotes: status.releaseNotes };
	}
	if (staged) return { kind: "install", version: staged.version };
	return null;
}

/**
 * Which update operation an error status ended, or null for a failed check.
 *
 * Most failures carry `errorInfo.phase`. AO's own sentences (a stalled download,
 * a failed macOS stage) do not, so for those the operation that was in flight
 * just before the error decides.
 */
export function updateFailurePhase(status: UpdateStatus, inFlight: FailedPhase | null): FailedPhase | null {
	if (status.state !== "error") return null;
	const phase = status.errorInfo?.phase;
	if (phase === "download" || phase === "install") return phase;
	if (phase) return null;
	return inFlight;
}

function inFlightPhase(status: UpdateStatus, previous: FailedPhase | null): FailedPhase | null {
	if (status.state === "downloading") return "download";
	if (status.state === "preparing") return "install";
	// An error keeps what was in flight so the failure can be attributed to it.
	if (status.state === "error") return previous;
	return null;
}

function useSidebarUpdateAction(status: UpdateStatus): SidebarUpdateAction {
	// Derived from the previous status during render, so the failed row lands on
	// the same frame as the error instead of one effect later.
	const [tracked, setTracked] = useState<{ status: UpdateStatus; inFlight: FailedPhase | null; failed: FailedPhase | null }>(
		() => ({ status, inFlight: inFlightPhase(status, null), failed: updateFailurePhase(status, null) }),
	);
	let current = tracked;
	if (tracked.status !== status) {
		current = {
			status,
			inFlight: inFlightPhase(status, tracked.inFlight),
			failed: updateFailurePhase(status, tracked.inFlight),
		};
		setTracked(current);
	}
	return sidebarUpdateAction(status, current.failed);
}

/** Base version for display: nightlies drop their long build stamp. */
function displayVersion(version: string | undefined): string | undefined {
	if (!version) return undefined;
	return parseNightlyVersion(version)?.base ?? version;
}

function nightlyCaption(version: string | undefined, t: TFunction, locale: string): string | null {
	const nightly = version ? parseNightlyVersion(version) : null;
	if (!nightly) return null;
	return t("shell.updateNightlyBuilt", {
		date: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(nightly.builtAt),
	});
}

function ProgressRing({ percent, className }: { percent?: number; className?: string }) {
	const radius = 6.25;
	const circumference = 2 * Math.PI * radius;
	return (
		<svg aria-hidden="true" className={cn("-rotate-90", className)} viewBox="0 0 16 16">
			<circle cx="8" cy="8" r={radius} fill="none" stroke="currentColor" strokeOpacity={0.25} strokeWidth={2} />
			<circle
				cx="8"
				cy="8"
				r={radius}
				fill="none"
				stroke="currentColor"
				strokeDasharray={`${(circumference * (percent ?? 0)) / 100} ${circumference}`}
				strokeLinecap="round"
				strokeWidth={2}
			/>
		</svg>
	);
}

function UpdatePill({ children, ...props }: React.ComponentProps<typeof Button>) {
	return (
		<Button shape="pill" size="xs" type="button" variant="accent-soft" {...props}>
			{children}
		</Button>
	);
}

/** Marks a failed update; the other states keep the slot empty. */
function FailedDot() {
	return <span aria-hidden="true" className="size-2 rounded-full bg-destructive" />;
}

/**
 * "0.14.11 is here" with the version underlined in dots when there is more to
 * read. Hovering or focusing the version shows what's new in that build.
 */
function AvailableTitle({
	nightly,
	notes,
	tabIndex,
	title,
	version,
}: {
	nightly: string | null;
	notes?: string;
	tabIndex: number;
	title: string;
	version: string;
}) {
	const { t } = useTranslation();
	const at = title.indexOf(version);
	if (at < 0 || (!notes && !nightly)) return <>{title}</>;
	return (
		<>
			{title.slice(0, at)}
			<HoverCard>
				<HoverCardTrigger asChild>
					<span
						className="cursor-default underline decoration-passive decoration-dotted underline-offset-[3px] outline-none focus-visible:rounded-xs focus-visible:ring-2 focus-visible:ring-ring"
						tabIndex={tabIndex}
					>
						{version}
					</span>
				</HoverCardTrigger>
				<HoverCardContent className="max-h-80 w-max max-w-80 overflow-y-auto p-3" side="top">
					<p className="text-sm font-medium text-foreground">
						{notes ? t("shell.updateWhatsNewVersion", { version }) : version}
					</p>
					{nightly ? <p className="mt-0.5 text-xs text-passive">{nightly}</p> : null}
					{notes ? (
						<div className="mt-2">
							<DesktopReleaseNotes notes={notes} textClassName="text-sm text-muted-foreground" />
						</div>
					) : null}
				</HoverCardContent>
			</HoverCard>
			{title.slice(at + version.length)}
		</>
	);
}

/**
 * The expanded footer's update row: one line saying what is happening, a
 * caption, and a single tinted pill that does the next thing. It renders
 * nothing while AO is up to date.
 */
export function SidebarUpdateRow({
	onRequestInstall,
	status,
	tabIndex,
}: {
	/** Opens the restart confirmation; installing outright would quit the app. */
	onRequestInstall: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t, i18n } = useTranslation();
	const locale = i18n.resolvedLanguage ?? i18n.language;
	const action = useSidebarUpdateAction(status);
	if (action === null) return null;

	// Empty for the calm states, so every title still lines up with Settings.
	let lead: React.ReactNode = null;
	let title: string;
	let titleContent: React.ReactNode = null;
	let caption: React.ReactNode = null;
	let pill: React.ReactNode = null;
	const rowProps: React.HTMLAttributes<HTMLDivElement> = {};

	if (action.kind === "download") {
		const version = displayVersion(action.version);
		title = version ? t("shell.updateIsHere", { version }) : t("shell.updateAvailable");
		if (version) {
			titleContent = (
				<AvailableTitle
					nightly={nightlyCaption(action.version, t, locale)}
					notes={action.releaseNotes}
					tabIndex={tabIndex}
					title={title}
					version={version}
				/>
			);
		}
		pill = (
			<UpdatePill
				aria-label={
					action.version
						? t("shell.downloadUpdateVersion", { version: action.version })
						: t("shell.downloadUpdate")
				}
				onClick={() => void requestUpdateDownload()}
				tabIndex={tabIndex}
			>
				{t("shell.updateAction")}
			</UpdatePill>
		);
	} else if (action.kind === "downloading") {
		title = t("shell.updateDownloading");
		lead = <ProgressRing className="size-3.5 text-logo-accent" percent={action.percent} />;
		Object.assign(rowProps, { "aria-live": "polite", role: "status" });
		caption = (
			<span
				aria-label={t("shell.updateDownloading")}
				aria-valuemax={100}
				aria-valuemin={0}
				aria-valuenow={action.percent === undefined ? undefined : Math.round(action.percent)}
				className="mt-1 mb-0.5 block h-0.75 overflow-hidden rounded-full bg-foreground/10"
				role="progressbar"
			>
				<span
					className="block h-full rounded-full bg-logo-accent transition-[width] duration-normal ease-out motion-reduce:transition-none"
					style={{ width: `${action.percent ?? 0}%` }}
				/>
			</span>
		);
	} else if (action.kind === "install") {
		const version = displayVersion(action.version);
		title = t("shell.updateReadyToInstall");
		caption = t("shell.updateRestartToFinish");
		pill = (
			<UpdatePill
				aria-label={
					version
						? t("shell.restartInstallUpdateVersion", { version })
						: t("shell.restartInstallUpdate")
				}
				onClick={onRequestInstall}
				tabIndex={tabIndex}
			>
				{t("shell.updateRestart")}
			</UpdatePill>
		);
	} else {
		title = t("shell.updateFailed");
		caption = action.phase === "install" ? t("shell.updateInstallFailed") : t("shell.updateDownloadFailed");
		lead = <FailedDot />;
		pill = (
			<UpdatePill
				aria-label={t("shell.retryUpdate")}
				onClick={action.phase === "install" ? onRequestInstall : () => void requestUpdateDownload()}
				tabIndex={tabIndex}
			>
				{t("shell.updateRetry")}
			</UpdatePill>
		);
	}

	const testId = {
		download: "sidebar-update-available",
		downloading: "sidebar-update-downloading",
		install: "sidebar-update-ready",
		failed: "sidebar-update-failed",
	}[action.kind];

	return (
		<div
			className="mb-0.5 flex min-h-9 w-full items-center py-1.5 pr-1.5 pl-2.5"
			data-testid={testId}
			{...rowProps}
		>
			{/* A one-line row centres its dot; a two-line row pins it to the title. */}
			<span className={cn("mr-2 grid size-4 shrink-0 place-items-center", caption && "self-start mt-0.5")}>{lead}</span>
			<span className="mr-2 flex min-w-0 flex-1 flex-col gap-0.5">
				{/* No native tooltip over the version's hover card. */}
				<span className="truncate text-sm font-medium tracking-tight text-foreground" title={titleContent ? undefined : title}>
					{titleContent ?? title}
				</span>
				{caption ? (
					<span className="truncate text-xs text-passive" title={typeof caption === "string" ? caption : undefined}>
						{caption}
					</span>
				) : null}
			</span>
			{pill}
		</div>
	);
}

/** Icon-rail variant of {@link SidebarUpdateRow} for the collapsed sidebar. */
export function SidebarUpdateRail({
	onRequestInstall,
	status,
	tabIndex,
}: {
	/** Opens the restart confirmation; installing outright would quit the app. */
	onRequestInstall: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t } = useTranslation();
	const action = useSidebarUpdateAction(status);
	if (action === null) return null;

	if (action.kind === "downloading") {
		const label = t("shell.updateDownloading");
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span
						aria-label={label}
						aria-live="polite"
						className="grid size-control-board place-items-center rounded-lg text-logo-accent"
						role="status"
					>
						<ProgressRing className="size-4" percent={action.percent} />
					</span>
				</TooltipTrigger>
				<TooltipContent side="right">{label}</TooltipContent>
			</Tooltip>
		);
	}

	let label: string;
	let tooltip: string;
	let onClick: () => void;
	let failed = false;
	if (action.kind === "download") {
		const version = displayVersion(action.version);
		label = action.version ? t("shell.downloadUpdateVersion", { version: action.version }) : t("shell.downloadUpdate");
		tooltip = `${version ? t("shell.updateIsHere", { version }) : t("shell.updateAvailable")} · ${t("shell.updateAction")}`;
		onClick = () => void requestUpdateDownload();
	} else if (action.kind === "install") {
		const version = displayVersion(action.version);
		label = version ? t("shell.restartInstallUpdateVersion", { version }) : t("shell.restartInstallUpdate");
		tooltip = `${t("shell.updateReadyToInstall")} · ${t("shell.updateRestart")}`;
		onClick = onRequestInstall;
	} else {
		failed = true;
		label = t("shell.retryUpdate");
		tooltip = `${t("shell.updateFailed")} · ${t("shell.updateRetry")}`;
		onClick = action.phase === "install" ? onRequestInstall : () => void requestUpdateDownload();
	}

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Pressable
					aria-label={label}
					className={cn(FOOTER_RAIL_BUTTON_CLASS, "text-foreground")}
					onClick={onClick}
					tabIndex={tabIndex}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] grid place-items-center">
						{failed ? <RotateCw aria-hidden="true" /> : <ArrowUp aria-hidden="true" />}
						<span
							aria-hidden="true"
							className={cn(
								"absolute -top-0.5 -right-1 size-1.5 rounded-full ring-2 ring-sidebar",
								failed ? "bg-destructive" : "bg-logo-accent",
							)}
						/>
					</span>
				</Pressable>
			</TooltipTrigger>
			<TooltipContent side="right">{tooltip}</TooltipContent>
		</Tooltip>
	);
}
