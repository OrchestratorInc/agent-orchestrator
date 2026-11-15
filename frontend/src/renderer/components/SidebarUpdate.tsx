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

export const FOOTER_RAIL_BUTTON_CLASS = cn(
	NAV_ROW_HIGHLIGHT_HOST_CLASS,
	"grid size-control-board place-items-center rounded-lg text-muted-foreground [&_svg]:size-icon-base",
);

type FailedPhase = "download" | "install";

export type UpdateRecovery = "install" | "check" | "download";

export type SidebarUpdateAction =
	| { kind: "downloading"; percent?: number }
	| { kind: "download"; version?: string }
	| { kind: "install"; version?: string }
	| { kind: "failed"; phase: FailedPhase; recovery: UpdateRecovery }
	| null;

export function sidebarUpdateAction(status: UpdateStatus, failedPhase: FailedPhase | null = null): SidebarUpdateAction {
	if (status.state === "downloading") {
		const percent = status.percent === undefined ? undefined : Math.min(100, Math.max(0, status.percent));
		return { kind: "downloading", percent };
	}
	const staged =
		status.staged ??
		(status.state === "downloaded" ? { version: status.version } : undefined);
	if (status.state === "error" && failedPhase) {
		const recovery = failedPhase === "download" ? "download" : staged ? "install" : "check";
		return { kind: "failed", phase: failedPhase, recovery };
	}
	if (status.state === "available" && status.version !== staged?.version) {
		return { kind: "download", version: status.version };
	}
	if (staged) return { kind: "install", version: staged.version };
	return null;
}

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
	if (status.state === "error") return previous;
	return null;
}

function useSidebarUpdateAction(status: UpdateStatus): SidebarUpdateAction {
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

function displayVersion(version: string | undefined): string | undefined {
	if (!version) return undefined;
	return parseNightlyVersion(version)?.base ?? version;
}

function shortVersion(version: string | undefined): string | null {
	const bare = displayVersion(version?.replace(/^v/i, ""));
	return bare ? `v${bare}` : null;
}

function ProgressRing({ percent, className }: { percent?: number; className?: string }) {
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

function UpdateGlyph({
	icon,
	percent,
	solid,
}: {
	icon: React.ReactNode;
	percent?: number;
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

export function SidebarUpdateRow({
	onRequestInstall,
	status,
	tabIndex,
}: {
	onRequestInstall: () => void;
	status: UpdateStatus;
	tabIndex: number;
}) {
	const { t } = useTranslation();
	const action = useSidebarUpdateAction(status);
	if (action === null) return null;

	let title: string;
	let versionNote: string | null = null;
	let detail: string | null = null;
	let glyph: React.ReactNode;
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

export function SidebarUpdateRail({
	onRequestInstall,
	status,
	tabIndex,
}: {
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
