import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowRightLeft, LoaderCircle, TriangleAlert } from "@/components/app-icons";
import { useHarnessUpdateNoticeClearance } from "../hooks/useHarnessUpdateNoticeClearance";
import { useMoveSession } from "../hooks/useMoveSession";
import { cancelWaitingMove, moveKey, showMove, useMoveStore, type MoveState } from "../lib/move-runner";
import { MOVE_PART_BYTES } from "../lib/move-session";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";

const MB = 1024 * 1024;

/** Refusals that the same move cannot get past by retrying. */
const FINAL_REFUSALS = new Set(["TRANSFER_DEFAULT_BRANCH", "TRANSFER_UNSUPPORTED", "SESSION_MOVED"]);

/** What the move is doing now, such as "Copying the conversation · 12 of 32 MB". */
export function useMoveStepText(move: MoveState | undefined): string {
	const { t } = useTranslation();
	if (!move || move.phase !== "moving") return move?.phase === "waiting" ? t("moveSession.waitingTitle") : "";
	const step = t(`moveSession.step.${move.step ?? "prepare"}`, { host: move.targetLabel });
	const progress = move.step === "copy" && move.progress && move.progress.total > MOVE_PART_BYTES ? move.progress : undefined;
	return progress
		? `${step} · ${t("moveSession.progress", { sent: Math.round(progress.sent / MB), total: Math.max(1, Math.round(progress.total / MB)) })}`
		: step;
}

/** The copy's real progress, from 0 to 1, while there is one. */
export function moveFraction(move: MoveState | undefined): number | undefined {
	const progress = move?.phase === "moving" && move.step === "copy" ? move.progress : undefined;
	return progress && progress.total > 0 ? Math.min(1, progress.sent / progress.total) : undefined;
}

/** A session's move, on the session: the mid-turn choice, progress, and a failure with its way out. */
export function MoveBanner({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const key = moveKey(session.hostId, session.id);
	const move = useMoveStore((state) => state.moves[key]);
	const actions = useMoveSession(session);
	const [busy, setBusy] = useState(false);
	const stepText = useMoveStepText(move);
	useEffect(() => showMove(key), [key]);
	if (!move || !actions || (move.phase === "done" && !move.result?.commitError)) return null;

	const host = move.targetLabel;
	const run = (work: () => Promise<void>) => {
		setBusy(true);
		void work().finally(() => setBusy(false));
	};
	const dismiss = <CloseButton aria-label={t("common.close")} onClick={actions.dismiss} size="icon-xs" />;
	// A new turn started before the move could: ask again rather than fail.
	if (move.phase === "asking" || (move.phase === "failed" && move.error?.code === "TRANSFER_AGENT_BUSY")) {
		return (
			<Row icon={<ArrowRightLeft aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />} title={t("moveSession.busyTitle")} detail={t("moveSession.busyBody")}>
				<Button size="sm" variant="secondary" onClick={() => actions.retry({ stopTurn: true })}>{t("moveSession.stopAndMove")}</Button>
				<Button size="sm" variant="primary" onClick={() => actions.retry({ afterTurn: true })}>{t("moveSession.afterTurn")}</Button>
				{dismiss}
			</Row>
		);
	}
	if (move.phase === "waiting" || move.phase === "moving") {
		const fraction = moveFraction(move);
		return (
			<div className="shrink-0">
				<Row icon={<LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-[var(--color-status-working)]" />} title={t("moveSession.moving", { host })} detail={stepText}>
					{move.phase === "waiting" ? <Button size="sm" variant="ghost" onClick={() => cancelWaitingMove(key)}>{t("moveSession.cancelMove")}</Button> : null}
				</Row>
				{fraction === undefined ? null : (
					<div className="h-0.5 bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(fraction * 100)}>
						<div className="h-full bg-[var(--color-status-working)] transition-[width] duration-150" style={{ width: `${fraction * 100}%` }} />
					</div>
				)}
			</div>
		);
	}
	const alert = <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-destructive" />;
	if (move.phase === "done") {
		return (
			<Row alert icon={alert} title={t("moveSession.commitFailed", { target: host, message: move.result?.commitError ?? "" })}>
				<Button size="sm" variant="secondary" disabled={busy} onClick={() => run(actions.retryCommit)}>{t("moveSession.retry")}</Button>
				{dismiss}
			</Row>
		);
	}
	const error = move.error;
	if (error?.code === "RECEIVE_UNCONFIRMED") {
		return (
			<Row alert icon={alert} title={t("moveSession.unconfirmed", { host })}>
				<Button size="sm" variant="ghost" disabled={busy} onClick={() => run(() => actions.keepHere(error.transferId))}>{t("moveSession.keepHere")}</Button>
				<Button size="sm" variant="secondary" onClick={() => actions.retry()}>{t("moveSession.retry")}</Button>
			</Row>
		);
	}
	return (
		<Row alert icon={alert} title={t("moveSession.failed", { host, message: error?.message ?? "" })} detail={error?.resumedSource ? t("moveSession.resumed") : t("moveSession.notResumed")}>
			{error?.code && FINAL_REFUSALS.has(error.code) ? null : <Button size="sm" variant="secondary" onClick={() => actions.retry()}>{t("moveSession.retry")}</Button>}
			{dismiss}
		</Row>
	);
}

function Row({ icon, title, detail, alert = false, children }: { icon: ReactNode; title: string; detail?: string; alert?: boolean; children?: ReactNode }) {
	const [row, setRow] = useState<HTMLElement | null>(null);
	const clearance = useHarnessUpdateNoticeClearance(row);
	return (
		<div ref={setRow} role={alert ? "alert" : "status"} aria-atomic="true" style={clearance ? { paddingRight: clearance + 16 } : undefined} className="flex shrink-0 items-center gap-2.5 border-b border-border bg-surface px-4 py-2" data-testid="move-banner">
			{icon}
			<p className={alert ? "min-w-0 flex-1 text-xs leading-snug" : "min-w-0 flex-1 truncate text-xs"}>
				<span className={alert ? "font-medium text-destructive" : "font-medium"}>{title}</span>
				{detail ? <span className="text-muted-foreground"> · {detail}</span> : null}
			</p>
			{children}
		</div>
	);
}
