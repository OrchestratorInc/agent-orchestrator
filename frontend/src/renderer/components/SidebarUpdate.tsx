import { useState } from "react";
import { ArrowDown, ArrowUp, RotateCcw, RotateCw } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { UpdateStatus } from "../../main/update-settings";
import { parseNightlyVersion } from "../lib/build-channel";
import { requestUpdateDownload } from "../hooks/useUpdateStatus";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** Collapsed footer icon-rail control: same growing highlight in the square. */
export const FOOTER_RAIL_BUTTON_CLASS = cn(
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"grid size-control-board place-items-center rounded-lg text-muted-foreground [&_svg]:size-icon-base",
);

type FailedPhase = "download" | "install";

/**
 * How Retry recovers. A failed install only reopens the restart confirmation
 * while a staged build is still there to install: a failed macOS stage
 * discards it, and the dialog would then have nothing to confirm. Without one,
 * a fresh check finds the build again.
 */
export type UpdateRecovery = "install" | "check" | "download";

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
	| { kind: "download"; version?: string }
	| { kind: "install"; version?: string }
	| { kind: "failed"; phase: FailedPhase; recovery: UpdateRecovery }
	| null;

export function sidebarUpdateAction(status: UpdateStatus, failedPhase: FailedPhase | null = null): SidebarUpdateAction {
	if (status.state === "downloading") {
		// Absent until the first progress event of a requested download.
		const percent = status.percent === undefined ? undefined : Math.min(100, Math.max(0, status.percent));
		return { kind: "downloading", percent };
	}
	// `staged` is the stamp the main process puts on every status; the
	// `downloaded` fallback keeps this correct for any status that predates it
	// or arrives from a source that does not stamp.
	const staged =
		status.staged ??
		(status.state === "downloaded" ? { version: status.version } : undefined);
	if (status.state === "error" && failedPhase) {
		const recovery = failedPhase === "download" ? "download" : staged ? "install" : "check";
		return { kind: "failed", phase: failedPhase, recovery };
	}
	// Something newer than what is already staged still deserves the download
	// action; the main process reports a re-discovered staged build as
	// "downloaded", so an "available" here is genuinely a different version.
	if (status.state === "available" && status.version !== staged?.version) {
		return { kind: "download", version: status.version };
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

/** "v0.15.0" for the row: one "v" whatever the feed sent, no nightly stamp. */
function shortVersion(version: string | undefined): string | null {
	const bare = displayVersion(version?.replace(/^v/i, ""));
	return bare ? `v${bare}` : null;
}

function ProgressRing({ percent, className }: { percent?: number; className?: string }) {
	// A hairline ring, about as heavy as the 1.25px icon strokes beside it.
	const radius = 6.75;
	const circumference = 2 * Math.PI * radius;
	return (
		<svg aria-hidden="true" className={cn("-rotate-90", className)} viewBox="0 0 16 16">
			<circle cx="8" cy="8" r={radius} fill="none" stroke="currentColor" strokeOpacity={0.25} strokeWidth={1.25} />
			<circle
				cx="8"
				cy="8"
				r={radius}
				fill="none"
				stroke="currentColor"
				className="transition-[stroke-dasharray] duration-normal ease-out motion-reduce:transition-none"
				strokeLinecap="round"
				strokeWidth={1.25}
				style={{ strokeDasharray: `${(circumference * (percent ?? 0)) / 100} ${circumference}` }}
			/>
		</svg>
	);
}

function recover(recovery: UpdateRecovery, onRequestInstall: () => void): void {
	if (recovery === "install") onRequestInstall();
	else if (recovery === "check") void aoBridge.updates.check();
	else void requestUpdateDownload();
}

/**
 * The update icon. Available and downloading draw an arrow in a ring, and while
 * a download runs the ring is its progress, filling as the bytes arrive. Once
 * there is nothing left to fill (ready, or failed) the circle is solid with the
 * icon cut out of it, so a circular icon such as restart never sits inside a
 * second circle.
 */
function UpdateGlyph({
	icon,
	percent,
	solid,
}: {
	icon: React.ReactNode;
	percent?: number;
	/** Solid disc: brand blue when ready, the destructive red when failed. */
	solid?: "ready" | "failed";
}) {
	const iconClass = "[&_svg]:size-3 [&_svg]:stroke-[2.5]";
	if (solid) {
		return (
			<span
				className={cn(
					"grid size-5 place-items-center rounded-full",
					solid === "ready" ? "bg-logo-accent text-logo-accent-foreground" : "bg-[var(--color-danger-strong)] text-white",
					iconClass,
				)}
			>
				{icon}
			</span>
		);
	}
	return (
		<span className="relative grid size-5 place-items-center text-logo-accent">
			<ProgressRing className="absolute inset-0 size-5" percent={percent} />
			<span className={cn("grid place-items-center", iconClass)}>{icon}</span>
		</span>
	);
}

/**
 * The expanded footer's update strip: one 36px row saying what is happening,
 * with the update icon at its end doing the next thing. Once a download starts
 * the icon's circle fills with its progress. It renders nothing while AO is up
 * to date.
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
	const { t } = useTranslation();
	const action = useSidebarUpdateAction(status);
	if (action === null) return null;

	let title: string;
	// Dimmed after the title: which version "Update available" means.
	let versionNote: string | null = null;
	// Why it failed: one row has no room for a second line, so it is the tooltip.
	let detail: string | null = null;
	let glyph: React.ReactNode;
	// The whole row is the button; a download in progress has nothing to press.
	let press: { label: string; onClick: () => void } | null = null;

	if (action.kind === "download") {
		title = t("shell.updateAvailable");
		versionNote = shortVersion(action.version);
		glyph = <UpdateGlyph icon={<ArrowDown aria-hidden="true" />} percent={100} />;
		press = {
			label: action.version ? t("shell.downloadUpdateVersion", { version: action.version }) : t("shell.downloadUpdate"),
			onClick: () => void requestUpdateDownload(),
		};
	} else if (action.kind === "downloading") {
		title = t("shell.updateDownloading");
		// The icon stays where it was; its circle is now the progress.
		glyph = (
			<span
				aria-label={t("shell.updateDownloading")}
				aria-valuemax={100}
				aria-valuemin={0}
				aria-valuenow={action.percent === undefined ? undefined : Math.round(action.percent)}
				role="progressbar"
			>
				<UpdateGlyph icon={<ArrowDown aria-hidden="true" />} percent={action.percent} />
			</span>
		);
	} else if (action.kind === "install") {
		const version = displayVersion(action.version);
		title = t("shell.updateReadyToInstall");
		glyph = <UpdateGlyph icon={<RotateCw aria-hidden="true" />} solid="ready" />;
		press = {
			label: version ? t("shell.restartInstallUpdateVersion", { version }) : t("shell.restartInstallUpdate"),
			onClick: onRequestInstall,
		};
	} else {
		title = t("shell.updateFailed");
		detail = action.phase === "install" ? t("shell.updateInstallFailed") : t("shell.updateDownloadFailed");
		glyph = <UpdateGlyph icon={<RotateCcw aria-hidden="true" />} solid="failed" />;
		const { recovery } = action;
		press = { label: t("shell.retryUpdate"), onClick: () => recover(recovery, onRequestInstall) };
	}

	const testId = {
		download: "sidebar-update-available",
		downloading: "sidebar-update-downloading",
		install: "sidebar-update-ready",
		failed: "sidebar-update-failed",
	}[action.kind];

	const content = (
		<>
			<span className="relative z-[1] min-w-0 flex-1 truncate text-[length:var(--font-size-base)] font-medium tracking-tight text-foreground">
				{title}
				{versionNote ? (
					<span className="ml-1.5 text-xs font-normal tabular-nums text-passive">{versionNote}</span>
				) : null}
				{detail ? <span className="sr-only">{` · ${detail}`}</span> : null}
			</span>
			<span className="relative z-[1] grid size-control-md shrink-0 place-items-center">{glyph}</span>
		</>
	);
	// A filled strip with no outline or shadow (DESIGN.md: grouped content), as
	// tall as the footer rows below it.
	const rowClass = "flex min-h-9 w-full items-center gap-2 rounded-lg bg-[var(--color-bg-secondary)] py-1 pr-1 pl-3 text-left";

	if (!press) {
		return (
			<div aria-live="polite" className={rowClass} data-testid={testId} role="status">
				{content}
			</div>
		);
	}
	return (
		<Pressable
			aria-label={press.label}
			title={detail ? `${title} · ${detail}` : undefined}
			// The strip is already filled, so hover steps the fill up a shade
			// instead of growing the nav-row pill inside it.
			className={cn(
				rowClass,
				"hover:bg-[var(--color-bg-tertiary)] focus-visible:bg-[var(--color-bg-tertiary)]",
			)}
			data-testid={testId}
			onClick={press.onClick}
			tabIndex={tabIndex}
			type="button"
		>
			{content}
		</Pressable>
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
		label = action.version ? t("shell.downloadUpdateVersion", { version: action.version }) : t("shell.downloadUpdate");
		tooltip = t("shell.updateAvailable");
		onClick = () => void requestUpdateDownload();
	} else if (action.kind === "install") {
		const version = displayVersion(action.version);
		label = version ? t("shell.restartInstallUpdateVersion", { version }) : t("shell.restartInstallUpdate");
		tooltip = t("shell.updateReadyToInstall");
		onClick = onRequestInstall;
	} else {
		failed = true;
		label = t("shell.retryUpdate");
		tooltip = t("shell.updateFailed");
		const { recovery } = action;
		onClick = () => recover(recovery, onRequestInstall);
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
