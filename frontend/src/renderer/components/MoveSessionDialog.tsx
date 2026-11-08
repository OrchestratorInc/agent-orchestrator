import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Trans, useTranslation } from "react-i18next";
import { Check, GitBranch, LoaderCircle, Monitor } from "@/components/app-icons";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { cancelWaitingMove, clearMove, moveKey, runMove, useMoveStore } from "../lib/move-runner";
import { commitMove, MOVE_PART_BYTES, type MoveResult, type MoveStep } from "../lib/move-session";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { hostsWithRepo } from "../lib/repo-hosts";
import { sessionBranchPresentation } from "../lib/session-branch";
import { useUiStore, type MoveSessionRequest } from "../stores/ui-store";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { MultiStepLoader } from "./ui/multi-step-loader";
import { Pressable } from "./ui/pressable";

const STEPS: MoveStep[] = ["prepare", "copy", "receive", "commit"];
const MB = 1024 * 1024;

/** Mounted once in the shell; the session menu opens it through the UI store. */
export function MoveSessionDialog() {
	const request = useUiStore((state) => state.moveSessionRequest);
	const clear = useUiStore((state) => state.clearMoveSessionRequest);
	if (!request) return null;
	return <MoveSessionDialogContent key={moveKey(request.hostId, request.sessionId)} request={request} onClose={clear} />;
}

function MoveSessionDialogContent({ request, onClose }: { request: MoveSessionRequest; onClose: () => void }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const local = useWorkspaceQuery().data ?? [];
	const remote = useRemoteWorkspaces().data ?? [];
	const key = moveKey(request.hostId, request.sessionId);
	const move = useMoveStore((state) => state.moves[key]);
	const [picked, setPicked] = useState<HostId>();
	const [committing, setCommitting] = useState(false);

	const sourceHost: HostId = request.hostId ?? LOCAL_HOST;
	const hostLabel = (host: HostId) => (host === LOCAL_HOST ? t("remote.thisComputer") : labelForHost(host) ?? host);
	const sourceProject = (sourceHost === LOCAL_HOST ? local : remote.filter((w) => w.hostId === sourceHost)).find((w) => w.id === request.projectId);
	const session = sourceProject?.sessions.find((each) => each.id === request.sessionId);
	const branch = session ? sessionBranchPresentation(session).branch : undefined;
	const targets = hostsWithRepo(local, remote, sourceProject?.repoKey).filter((host) => host.hostId !== sourceHost);
	const target = targets.find((host) => host.hostId === (move?.targetHostId ?? picked ?? targets[0]?.hostId));
	const source = { client: clientForSessionHost(request.hostId), hostId: sourceHost, sessionId: request.sessionId, label: hostLabel(sourceHost) };
	const destination = target && {
		client: clientForSessionHost(target.hostId === LOCAL_HOST ? undefined : target.hostId),
		hostId: target.hostId,
		label: hostLabel(target.hostId),
		projectId: target.projectId,
	};

	const turnRunning = session?.activity?.state === "active" || session?.activity?.state === "blocked";
	const view = !move
		? turnRunning ? "busy" : "choose"
		: move.phase === "failed"
			? move.error?.code === "TRANSFER_AGENT_BUSY" ? "busy" : "failed"
			: move.phase === "done"
				? "commitFailed"
				: move.phase;

	const refresh = (hostId: HostId) => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(request.hostId) });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId === LOCAL_HOST ? undefined : hostId) });
	};
	const dialogOpen = () => useUiStore.getState().moveSessionRequest?.sessionId === request.sessionId;

	const start = (options: { afterTurn?: boolean; stopTurn?: boolean } = {}) => {
		if (!destination) return;
		const dest = destination;
		const open = (result: MoveResult) => void navigate(sessionNavigateTarget(dest.projectId, result.sessionId, dest.hostId));
		void runMove(key, { source, target: dest }, {
			...options,
			onDone: (result) => {
				refresh(dest.hostId);
				if (!result.committed) {
					if (!dialogOpen()) showGlobalToast(t("moveSession.toastFailed", { title: request.title }), result.commitError, { tone: "error" });
					return;
				}
				clearMove(key);
				const body = result.resumed ? undefined : t("moveSession.summaryBody");
				const title = t("moveSession.toastDone", { host: dest.label });
				// Success moves the person along: to the session if they waited, a toast with Open if they left.
				if (dialogOpen()) {
					onClose();
					open(result);
					showGlobalToast(title, body, { tone: "success" });
				} else {
					showGlobalToast(title, body, { tone: "success", action: { label: t("moveSession.open"), run: () => open(result) } });
				}
			},
			onFail: (error) => {
				refresh(dest.hostId);
				if (!dialogOpen()) showGlobalToast(t("moveSession.toastFailed", { title: request.title }), error.message, { tone: "error" });
			},
		});
	};

	const retryCommit = async () => {
		const result = move?.result;
		if (!result || !destination) return;
		setCommitting(true);
		const commitError = await commitMove(source, { hostId: destination.hostId, label: result.destinationLabel }, result.transferId, result.sessionId);
		setCommitting(false);
		if (commitError) {
			useMoveStore.setState((state) => ({ moves: { ...state.moves, [key]: { ...state.moves[key], result: { ...result, commitError } } } }));
			return;
		}
		clearMove(key);
		refresh(destination.hostId);
		onClose();
		void navigate(sessionNavigateTarget(destination.projectId, result.sessionId, destination.hostId));
	};

	const close = () => {
		// A queued or running move carries on; a settled failure is forgotten.
		if (move?.phase === "failed") clearMove(key);
		onClose();
	};

	const copyIndex = STEPS.indexOf("copy");
	const progress = move?.progress;

	return (
		<Dialog open onOpenChange={(open) => !open && close()}>
			<DialogContent className={settingsDialogContentClass}>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle>{t("moveSession.title")}</DialogTitle>
					<DialogDescription className="truncate">{request.title}</DialogDescription>
				</div>
				<div className={settingsDialogBodyClass}>
					{view === "waiting" || view === "moving" ? null : targets.length === 0 ? (
						<p className="text-sm text-muted-foreground">{t("moveSession.none")}</p>
					) : (
						<div role="radiogroup" aria-label={t("moveSession.pick")} className="overflow-hidden rounded-lg bg-[var(--color-bg-import-card)]">
							{targets.map((host) => {
								const selected = host.hostId === target?.hostId;
								return (
									<Pressable
										key={host.hostId}
										role="radio"
										aria-checked={selected}
										disabled={Boolean(move)}
										onClick={() => setPicked(host.hostId)}
										className="flex h-10 w-full items-center gap-2.5 border-t border-border px-3 text-sm first:border-t-0 hover:bg-interactive-hover aria-checked:bg-interactive-active disabled:cursor-default"
									>
										<Monitor aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
										<span className="min-w-0 truncate">{hostLabel(host.hostId)}</span>
										<span className="ml-auto truncate text-xs text-muted-foreground">{host.projectName}</span>
										<Check aria-hidden="true" className={selected ? "size-4 shrink-0" : "size-4 shrink-0 opacity-0"} />
									</Pressable>
								);
							})}
						</div>
					)}

					{(view === "choose" || view === "busy") && destination && branch ? (
						<p className="flex items-center gap-1.5 text-xs text-muted-foreground">
							<GitBranch aria-hidden="true" className="size-3.5 shrink-0" />
							<span className="min-w-0">
								<Trans t={t} i18nKey="moveSession.pushNote" values={{ branch }} components={{ branch: <span className="font-mono text-foreground/80" /> }} />
							</span>
						</p>
					) : null}

					{view === "busy" && destination ? (
						<StatusRow title={t("moveSession.busyTitle")} body={t("moveSession.busyBody")} />
					) : null}

					{view === "waiting" && destination ? (
						<StatusRow title={t("moveSession.waitingTitle")} body={t("moveSession.waitingBody", { host: destination.label })} />
					) : null}

					{view === "moving" && destination ? (
						<div className="flex flex-col gap-4">
							<MultiStepLoader
								ariaLabel={t("moveSession.title")}
								className="w-full max-w-none"
								activeIndex={Math.max(0, STEPS.indexOf(move?.step ?? "prepare"))}
								steps={STEPS.map((step) => t(`moveSession.step.${step}`, { host: destination.label }))}
								stepAccessories={STEPS.map((_, index) =>
									index === copyIndex && progress && progress.total > MOVE_PART_BYTES ? (
										<span className="pl-3 text-xs tabular-nums text-muted-foreground">
											{t("moveSession.progress", { sent: Math.round(progress.sent / MB), total: Math.max(1, Math.round(progress.total / MB)) })}
										</span>
									) : null,
								)}
							/>
							<p className="text-xs text-muted-foreground">{t("moveSession.closeHint")}</p>
						</div>
					) : null}

					{view === "failed" && move?.error ? (
						<p role="alert" className="text-xs leading-5 text-destructive">
							{t("moveSession.failed", { message: move.error.message })}{" "}
							<span className="text-muted-foreground">
								{move.error.resumedSource ? t("moveSession.resumed") : t("moveSession.notResumed")}
							</span>
						</p>
					) : null}

					{view === "commitFailed" && move?.result ? (
						<p role="alert" className="text-xs leading-5 text-destructive">
							{t("moveSession.commitFailed", { target: destination?.label ?? "", message: move.result.commitError ?? "" })}
						</p>
					) : null}
				</div>

				{view === "moving" ? null : (
					<div className={settingsDialogFooterClass}>
						{targets.length === 0 && !move ? (
							<Button variant="footer" onClick={close}>{t("moveSession.cancel")}</Button>
						) : view === "choose" ? (
							<>
								<Button variant="footer" onClick={close}>{t("moveSession.cancel")}</Button>
								<Button variant="footer-primary" disabled={!destination} onClick={() => start()}>{t("moveSession.start")}</Button>
							</>
						) : view === "busy" ? (
							<>
								<Button variant="footer" disabled={!destination} onClick={() => start({ stopTurn: true })}>{t("moveSession.stopAndMove")}</Button>
								<Button variant="footer-primary" disabled={!destination} onClick={() => start({ afterTurn: true })}>{t("moveSession.afterTurn")}</Button>
							</>
						) : view === "waiting" ? (
							<Button variant="footer" onClick={() => cancelWaitingMove(key)}>{t("moveSession.cancelMove")}</Button>
						) : view === "failed" ? (
							<>
								<Button variant="footer" onClick={close}>{t("moveSession.cancel")}</Button>
								<Button variant="footer-primary" disabled={!destination} onClick={() => start()}>{t("moveSession.retry")}</Button>
							</>
						) : (
							<Button variant="footer-primary" disabled={committing} onClick={() => void retryCommit()}>{t("moveSession.retry")}</Button>
						)}
					</div>
				)}
			</DialogContent>
		</Dialog>
	);
}

/** A working spinner with what is happening and what comes next. */
function StatusRow({ title, body }: { title: string; body: string }) {
	return (
		<div role="status" className="flex items-start gap-2.5 rounded-lg bg-[var(--color-bg-import-card)] px-3 py-2.5">
			<LoaderCircle aria-hidden="true" className="mt-0.5 size-4 shrink-0 animate-spin text-[var(--color-status-working)]" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="text-sm font-medium">{title}</span>
				<span className="text-xs text-muted-foreground">{body}</span>
			</div>
		</div>
	);
}
