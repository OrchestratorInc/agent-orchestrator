import {
	ArrowRightLeft,
	Loader2,
	MessageSquare,
	SquareTerminal,
	TriangleAlert,
	X,
} from "lucide-react";
import { type CSSProperties, type ReactNode, useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import type {
	SessionInterfaceMode,
	SessionInterfaceTransition,
	SessionInterfaceTransitionPolicy,
} from "../hooks/useSessionInterfaceTransition";
import {
	interfaceTransitionIsActive,
	interfaceTransitionIsCancellable,
	interfaceTransitionNeedsRestart,
} from "../hooks/useSessionInterfaceTransition";
import { useHarnessUpdateNoticeClearance } from "../hooks/useHarnessUpdateNoticeClearance";
import type { ViewportRect } from "../hooks/useLiveBrowserViewportRect";
import { cn } from "../lib/utils";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
import { DropdownMenuItem } from "./ui/dropdown-menu";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "./ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

function targetLabel(target: SessionInterfaceMode): string {
	return target === "chat" ? "chat UI" : "terminal UI";
}

function targetTitleLabel(target: SessionInterfaceMode): string {
	return target === "chat" ? "Chat UI" : "Terminal UI";
}

const phaseCopy: Record<SessionInterfaceTransition["phase"], string> = {
	requested: "Preparing switch…",
	preflighting: "Checking interface…",
	draining: "Waiting to switch…",
	source_stopping: "Stopping controller…",
	source_stopped: "Controller stopped…",
	target_starting: "Resuming agent…",
	activating: "Activating interface…",
	completed: "Interface switched",
	failed: "Interface switch failed",
	cancelled: "Interface switch cancelled",
	recovery_required: "Interface switch needs attention",
};

// An account move is the same durable handoff in the same interface; it names
// the account change instead of a destination interface.
const accountPhaseCopy: Record<SessionInterfaceTransition["phase"], string> = {
	requested: "Preparing account switch…",
	preflighting: "Checking the account…",
	draining: "Waiting for the turn to finish…",
	source_stopping: "Switching account…",
	source_stopped: "Switching account…",
	target_starting: "Resuming on the new account…",
	activating: "Switching account…",
	completed: "Account switched",
	failed: "Account switch failed",
	cancelled: "Account switch cancelled",
	recovery_required: "Account switch needs attention",
};

const targetStopUnconfirmedDetail =
	"AO could not confirm the target controller stopped. Restart AO to retry shutdown before restoring the original interface.";

export function SessionInterfaceSwitchButton({
	target,
	supported,
	disabledReason,
	pending,
	transition,
	cancelling,
	cancelError,
	onClick,
	onCancel,
	showLabel = false,
	className,
}: {
	target: SessionInterfaceMode;
	supported: boolean;
	disabledReason?: string;
	pending?: boolean;
	transition?: SessionInterfaceTransition;
	cancelling?: boolean;
	cancelError?: string;
	onClick: () => void;
	onCancel?: () => void;
	/** Cloud uses a text label so the return-to-TUI action is discoverable. */
	showLabel?: boolean;
	className?: string;
}) {
	if (transition && interfaceTransitionIsActive(transition)) {
		const needsRestart = interfaceTransitionNeedsRestart(transition);
		const cancellable = !needsRestart && interfaceTransitionIsCancellable(transition) && Boolean(onCancel);
		const accountMove = Boolean(transition.targetAccountId);
		const statusLabel = needsRestart
			? `${accountMove ? "Account" : "Interface"} switch needs attention. ${transition.errorDetail || targetStopUnconfirmedDetail}`
			: cancelError || (accountMove
				? accountPhaseCopy[transition.phase]
				: `${phaseCopy[transition.phase]} Switching to ${targetTitleLabel(transition.targetMode)}.`);
		const cancelLabel = accountMove ? "Cancel account switch" : `Cancel switch to ${targetTitleLabel(transition.targetMode)}`;
		return (
			<div
				role="status"
				aria-live="polite"
				aria-label={statusLabel}
				className={cn("relative inline-flex size-7 shrink-0 items-center justify-center text-muted-foreground", className)}
				title={statusLabel}
			>
				{/* TerminalTabFrame is a Tailwind `group`; tab hover swaps spinner → cancel. */}
				{needsRestart ? (
					<TriangleAlert aria-hidden="true" className="size-3.5 text-warning" />
				) : (
					<Loader2
						aria-hidden="true"
						className={cn(
							"size-3.5 animate-spin",
							cancellable && "pointer-events-none group-hover:opacity-0",
						)}
					/>
				)}
				{cancellable ? (
					<button
						type="button"
						aria-label={cancelLabel}
						title={cancelling ? "Cancelling…" : cancelLabel}
						disabled={cancelling}
						onClick={(event) => {
							event.stopPropagation();
							onCancel?.();
						}}
						className={cn(
							"absolute inset-0 inline-flex items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50 group-hover:opacity-100",
							cancelling && "opacity-100",
						)}
					>
						{cancelling ? (
							<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
						) : (
							<X aria-hidden="true" className="size-3.5" />
						)}
					</button>
				) : null}
			</div>
		);
	}

	const label = `Switch to ${targetLabel(target)}`;
	const tooltipLabel = supported ? `${label} using this agent's native conversation` : disabledReason || label;
	const TargetIcon = target === "chat" ? MessageSquare : SquareTerminal;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex">
					<TopbarButton
						aria-label={label}
						className={cn(
							!supported && "opacity-50",
							showLabel && "topbar-control--labeled !inline-flex !h-control-lg !w-auto !min-w-0 gap-1.5 px-2 text-xs",
							className,
						)}
						disabled={!supported || pending}
						onClick={onClick}
						type="button"
						variant="icon"
					>
						{pending ? (
							<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
						) : (
							<TargetIcon aria-hidden="true" className="size-icon-md" />
						)}
						{showLabel ? <span data-compact-label>{target === "tui" ? "Terminal UI" : "Chat UI"}</span> : null}
					</TopbarButton>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{tooltipLabel}</TooltipContent>
		</Tooltip>
	);
}

export function SessionInterfaceSwitchDialog({
	open,
	target,
	requireExplicitTerminalStop,
	waitingForInput,
	busy,
	error,
	onOpenChange,
	onChoose,
}: {
	open: boolean;
	target: SessionInterfaceMode;
	requireExplicitTerminalStop?: boolean;
	waitingForInput?: boolean;
	busy?: boolean;
	error?: string;
	onOpenChange: (open: boolean) => void;
	onChoose: (policy: SessionInterfaceTransitionPolicy) => void;
}) {
	const targetName = targetTitleLabel(target);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-w-md gap-0 overflow-hidden rounded-xl border-border bg-popover p-0">
				<DialogHeader className="border-b border-border px-5 py-4">
					<DialogTitle className="flex items-center gap-2 text-sm">
						<ArrowRightLeft aria-hidden="true" className="size-4 text-muted-foreground" />
						Switch to {targetName}?
					</DialogTitle>
					<DialogDescription className="pt-1 text-xs leading-5">
						The same AO session, worktree, and agent-native conversation continue in the other interface.
						Completed messages and tool work stay in the agent's context.
					</DialogDescription>
				</DialogHeader>

				<div className="grid gap-2 px-5 py-4">
					{requireExplicitTerminalStop ? (
						<p className="text-xs leading-5 text-muted-foreground">
							AO cannot verify whether this Terminal is idle. Nothing will stop until you confirm.
							If work is underway, switching will interrupt it.
						</p>
					) : (
						<button
							type="button"
							className="rounded-lg border border-border bg-background px-3.5 py-3 text-left transition-colors hover:border-border-strong hover:bg-muted disabled:opacity-50"
							disabled={busy}
							onClick={() => onChoose("drain")}
						>
							<strong className="block text-sm font-medium text-foreground">Finish work, then switch</strong>
							<span className="mt-1 block text-xs leading-5 text-muted-foreground">
								Wait for the running turn and anything already queued to finish. New AO messages wait safely
								for {targetName}.
							</span>
						</button>
					)}
					<button
						type="button"
						className="rounded-lg border border-border bg-background px-3.5 py-3 text-left transition-colors hover:border-warning/60 hover:bg-muted disabled:opacity-50"
						disabled={busy}
						onClick={() => onChoose("interrupt")}
					>
						<strong className="block text-sm font-medium text-foreground">
							{requireExplicitTerminalStop ? "Terminate and then switch" : "Stop now and switch"}
						</strong>
						<span className="mt-1 block text-xs leading-5 text-muted-foreground">
							{requireExplicitTerminalStop
								? "Stop the Terminal controller before switching. Any work still running will be interrupted. Files already changed remain in the worktree."
								: "Cancel the running turn before switching. Files already changed remain in the worktree, but unfinished output and queued Chat turns are cancelled."}
							{target === "chat" ? " Any unsent Terminal UI draft is discarded." : null}
						</span>
					</button>
					{waitingForInput && !requireExplicitTerminalStop ? (
						<p className="text-[11px] leading-4 text-warning">
							This turn is waiting for your input. “Finish work” will wait until you answer it; use “Stop
							now” to switch immediately.
						</p>
					) : null}
					{target === "tui" ? (
						<p className="text-[11px] leading-4 text-warning">
							Any unsent Chat draft or staged attachments are discarded when the switch completes.
						</p>
					) : null}
					{error ? (
						<p role="alert" className="text-xs leading-5 text-destructive">
							{error}
						</p>
					) : null}
				</div>

				<DialogFooter className="border-t border-border px-5 py-3">
					<Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
						Keep current interface
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

// A Cloud session created in Chat whose workspace could not start Chat yet
// starts in the terminal instead; the control plane records that as a failed
// tui-to-chat transition carrying one of these codes. It is information, not
// a failed switch.
function interfaceStartFallbackMessageKey(transition: SessionInterfaceTransition) {
	if (transition.phase !== "failed" || transition.sourceMode !== "tui" || transition.targetMode !== "chat") {
		return undefined;
	}
	switch (transition.errorCode) {
		case "CHAT_ADAPTER_UNAVAILABLE":
			return "session.interfaceStartFallbackAdapter" as const;
		case "CHAT_START_UNSUPPORTED":
			return "session.interfaceStartFallbackUnsupported" as const;
		default:
			return undefined;
	}
}

export function interfaceTransitionOffersHistoryRecovery(transition?: SessionInterfaceTransition): boolean {
	return interfaceTransitionHistoryRecoveryPolicy(transition) !== undefined;
}

function interfaceTransitionHistoryRecoveryPolicy(
	transition?: SessionInterfaceTransition,
): "strict" | "provider_history" | undefined {
	if (transition?.sourceMode !== "tui" || transition.targetMode !== "chat") return undefined;
	if (
		(transition.phase === "failed" && transition.errorCode === "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH") ||
		(transition.phase === "recovery_required" && transition.errorCode === "DAEMON_RESTARTED" &&
			transition.historyPolicy === "provider_history")
	) return "provider_history";
	if (transition.phase === "failed" && transition.errorCode === "TARGET_HISTORY_UNSETTLED") return "strict";
	return undefined;
}

/** The notice's widest size: w-[min(34rem,calc(100%-1.5rem))]. */
const TRANSITION_NOTICE_WIDTH_REM = 34;
const TRANSITION_NOTICE_GUTTER_REM = 1.5;

/** Where the notice sits centered in `pane`, before any clearance moves it. */
function centeredTransitionNoticeBox(pane: HTMLElement, notice: HTMLElement): ViewportRect {
	const paneBox = pane.getBoundingClientRect();
	const rem = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
	const width = Math.max(0, Math.min(TRANSITION_NOTICE_WIDTH_REM * rem, paneBox.width - TRANSITION_NOTICE_GUTTER_REM * rem));
	const left = paneBox.left + (paneBox.width - width) / 2;
	// The move is horizontal only, so the notice's own top and bottom stand.
	const { top, bottom } = notice.getBoundingClientRect();
	return { left, top, right: left + width, bottom };
}

export function SessionInterfaceTransitionNotice({
	transition,
	onDismiss,
	dismissing,
	dismissError,
	onSwitchWithInterrupt,
	interrupting,
	onRetry,
	retrying,
	onUseProviderHistory,
	recoveryError,
}: {
	transition?: SessionInterfaceTransition;
	onDismiss: () => void;
	dismissing?: boolean;
	dismissError?: string;
	onSwitchWithInterrupt?: () => void;
	interrupting?: boolean;
	onRetry?: () => void;
	retrying?: boolean;
	onUseProviderHistory?: () => void;
	recoveryError?: string;
}) {
	const { t } = useTranslation();
	// Centered at the top of the session pane, under the harness update notice
	// pinned to the app's top-right edge in a narrow window: then it centers in
	// the part of the pane the notice leaves clear, so its dismiss stays reachable.
	const [noticeElement, setNoticeElement] = useState<HTMLDivElement | null>(null);
	const pane = noticeElement?.offsetParent instanceof HTMLElement ? noticeElement.offsetParent : null;
	// Move only when the notice, where it centers without the move, would sit
	// under the update notice; a wide pane leaves it clear in place.
	const centeredNoticeBox = useCallback(
		(paneElement: HTMLElement) => (noticeElement ? centeredTransitionNoticeBox(paneElement, noticeElement) : null),
		[noticeElement],
	);
	const clearance = useHarnessUpdateNoticeClearance(pane, centeredNoticeBox);
	const placement: CSSProperties | undefined = clearance
		? { left: `calc((100% - ${clearance}px) / 2)`, maxWidth: `calc(100% - 1.5rem - ${clearance}px)` }
		: undefined;
	const needsRestart = interfaceTransitionNeedsRestart(transition);
	if (
		!transition ||
		(!needsRestart &&
			(transition.noticeAcknowledgedAt ||
				(transition.phase !== "failed" && transition.phase !== "recovery_required")))
	) {
		return null;
	}
	const startFallbackMessageKey = interfaceStartFallbackMessageKey(transition);
	if (startFallbackMessageKey) {
		return (
			<div
				ref={setNoticeElement}
				role="status"
				aria-live="polite"
				aria-atomic="true"
				style={placement}
				className="absolute left-1/2 top-3 z-20 flex w-[min(34rem,calc(100%-1.5rem))] -translate-x-1/2 items-start gap-2 rounded-lg border border-border bg-popover px-3 py-2.5 shadow-md"
			>
				<SquareTerminal aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
				<div className="min-w-0 flex-1">
					<strong className="block text-xs font-medium text-foreground">
						{t("session.interfaceStartFallbackTitle")}
					</strong>
					<p className="mt-0.5 text-caption leading-4 text-muted-foreground">{t(startFallbackMessageKey)}</p>
					{dismissError ? (
						<p className="mt-1 text-caption leading-4 text-destructive">{t("session.dismissInterfaceNoticeFailed")}</p>
					) : null}
				</div>
				<NoticeDismissButton dismissing={dismissing} onDismiss={onDismiss} />
			</div>
		);
	}
	const recovered =
		transition.phase === "recovery_required" && transition.errorCode === "DAEMON_RESTARTED";
	const accountMove = Boolean(transition.targetAccountId);
	const copy = accountMove ? accountPhaseCopy : phaseCopy;
	const historyRecoveryPolicy = interfaceTransitionHistoryRecoveryPolicy(transition);
	const interactiveSourceUnverified =
		transition.phase === "failed" &&
		transition.sourceMode === "tui" &&
		transition.targetMode === "chat" &&
		transition.errorCode === "SOURCE_DRAIN_FAILED" &&
		transition.errorDetail?.startsWith("source controller activity cannot be verified;");
	return (
		<div
			ref={setNoticeElement}
			role={recovered ? "status" : "alert"}
			style={placement}
			aria-live={recovered ? "polite" : "assertive"}
			aria-atomic="true"
			className={cn(
				"absolute left-1/2 top-3 z-20 flex w-[min(34rem,calc(100%-1.5rem))] -translate-x-1/2 items-start gap-2 rounded-lg border bg-popover px-3 py-2.5 shadow-md",
				recovered ? "border-border" : "border-warning/30",
			)}
		>
			{!recovered ? (
				<TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />
			) : null}
			<div className="min-w-0 flex-1">
				<strong className="block text-xs font-medium text-foreground">
					{needsRestart
						? copy.recovery_required
						: recovered
							? accountMove ? "Account switch recovered" : "Interface switch recovered"
							: copy[transition.phase]}
				</strong>
				<p className="mt-0.5 text-caption leading-4 text-muted-foreground">
					{(interactiveSourceUnverified
						? "AO cannot verify whether this Terminal is idle. It was left running; terminate it to switch interfaces."
						: transition.errorDetail) ||
						(needsRestart
							? targetStopUnconfirmedDetail
							: recovered
								? accountMove ? "The session stays on its previous account." : "AO restored the session in its last committed interface."
								: transition.phase === "recovery_required"
									? "Restart AO to reconcile this session before sending more work."
									: accountMove
										? "The agent stays on its previous account. You can retry the switch."
										: "The original interface remains available. You can retry the switch.")}
				</p>
				{transition.phase === "failed" && !accountMove &&
				(interactiveSourceUnverified || transition.errorCode === "DRAIN_DECISION_PENDING") &&
				onSwitchWithInterrupt ? (
					<Button
						type="button"
						size="sm"
						variant="outline"
						className="mt-2 h-7 text-caption"
						disabled={interrupting}
						onClick={onSwitchWithInterrupt}
					>
						{interrupting ? <Loader2 aria-hidden="true" className="size-3 animate-spin" /> : null}
						{interactiveSourceUnverified ? "Terminate and then switch" : "Cancel request and switch"}
					</Button>
				) : null}
				{historyRecoveryPolicy && onRetry ? (
					<div className="mt-2 flex flex-wrap items-center gap-2">
						<Button
							type="button"
							size="sm"
							variant="outline"
							className="h-7 text-caption"
							disabled={retrying || dismissing}
							onClick={onRetry}
						>
							{retrying ? <Loader2 aria-hidden="true" className="size-3 animate-spin" /> : null}
							Retry switch to Chat UI
						</Button>
						{historyRecoveryPolicy === "provider_history" && onUseProviderHistory ? (
							<Button
								type="button"
								size="sm"
								variant="outline"
								className="h-7 text-caption"
								disabled={retrying || dismissing}
								onClick={onUseProviderHistory}
							>
								Use provider history and switch
							</Button>
						) : null}
						<Button
							type="button"
							size="sm"
							variant="ghost"
							className="h-7 text-caption"
							disabled={retrying || dismissing}
							onClick={onDismiss}
						>
							Stay in Terminal
						</Button>
					</div>
				) : null}
				{recoveryError && !needsRestart ? (
					<p className="mt-1 text-caption leading-4 text-destructive">
						Recovery attempt failed: {recoveryError}
					</p>
				) : null}
				{dismissError && !needsRestart ? (
					<p className="mt-1 text-caption leading-4 text-destructive">{t("session.dismissInterfaceNoticeFailed")}</p>
				) : null}
			</div>
			{!needsRestart ? (
				<NoticeDismissButton dismissing={dismissing} onDismiss={onDismiss} />
			) : null}
		</div>
	);
}

function NoticeDismissButton({ dismissing, onDismiss }: { dismissing?: boolean; onDismiss: () => void }) {
	const { t } = useTranslation();
	return (
		<Button
			type="button"
			variant="ghost"
			size="icon-sm"
			className="size-control-xs rounded text-muted-foreground hover:text-foreground"
			onClick={onDismiss}
			disabled={dismissing}
			aria-label={t("session.dismissInterfaceNotice")}
		>
			{dismissing ? (
				<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
			) : (
				<X aria-hidden="true" className="size-3.5" />
			)}
		</Button>
	);
}

export function SessionInterfaceActionGroup({ children }: { children: ReactNode }) {
	return <div className="inline-flex shrink-0 items-center gap-2">{children}</div>;
}

export function SessionInterfaceSwitchMenuItem({
	target,
	supported,
	disabledReason,
	pending,
	onClick,
}: {
	target: SessionInterfaceMode;
	supported: boolean;
	disabledReason?: string;
	pending?: boolean;
	onClick: () => void;
}) {
	const label = `Switch to ${targetLabel(target)}`;
	const TargetIcon = target === "chat" ? MessageSquare : SquareTerminal;
	return (
		<DropdownMenuItem
			disabled={!supported || pending}
			onSelect={onClick}
			title={supported ? label : disabledReason || label}
		>
			{pending ? (
				<Loader2 aria-hidden="true" className="size-icon-lg animate-spin" />
			) : (
				<TargetIcon aria-hidden="true" className="size-icon-lg" />
			)}
			{label}
		</DropdownMenuItem>
	);
}
