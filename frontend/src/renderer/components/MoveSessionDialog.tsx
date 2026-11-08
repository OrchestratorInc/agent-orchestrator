import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Check, Circle, Loader2 } from "@/components/app-icons";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { commitMove, moveSession, MoveError, type MoveResult, type MoveStep } from "../lib/move-session";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { hostsWithRepo } from "../lib/repo-hosts";
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

const STEPS: MoveStep[] = ["prepare", "copy", "receive", "commit"];

/** Mounted once in the shell; the session menu opens it through the UI store. */
export function MoveSessionDialog() {
	const request = useUiStore((state) => state.moveSessionRequest);
	const clear = useUiStore((state) => state.clearMoveSessionRequest);
	if (!request) return null;
	return <MoveSessionDialogContent key={`${request.hostId ?? LOCAL_HOST}:${request.sessionId}`} request={request} onClose={clear} />;
}

function MoveSessionDialogContent({ request, onClose }: { request: MoveSessionRequest; onClose: () => void }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const local = useWorkspaceQuery().data ?? [];
	const remote = useRemoteWorkspaces().data ?? [];
	const sourceHost: HostId = request.hostId ?? LOCAL_HOST;
	const hostLabel = (host: HostId) => (host === LOCAL_HOST ? t("remote.thisComputer") : labelForHost(host) ?? host);
	const sourceProject = (sourceHost === LOCAL_HOST ? local : remote.filter((w) => w.hostId === sourceHost)).find((w) => w.id === request.projectId);
	const targets = hostsWithRepo(local, remote, sourceProject?.repoKey).filter((host) => host.hostId !== sourceHost);
	const [targetHost, setTargetHost] = useState<HostId | undefined>();
	const target = targets.find((host) => host.hostId === (targetHost ?? targets[0]?.hostId));
	const [step, setStep] = useState<MoveStep>();
	const [running, setRunning] = useState(false);
	const [error, setError] = useState<MoveError>();
	const [result, setResult] = useState<MoveResult>();
	const source = { client: clientForSessionHost(request.hostId), hostId: sourceHost, sessionId: request.sessionId, label: hostLabel(sourceHost) };
	const destination = target && {
		client: clientForSessionHost(target.hostId === LOCAL_HOST ? undefined : target.hostId),
		hostId: target.hostId,
		label: hostLabel(target.hostId),
		projectId: target.projectId,
	};

	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(request.hostId) });
		if (target) void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(target.hostId === LOCAL_HOST ? undefined : target.hostId) });
	};
	const start = async () => {
		if (!destination) return;
		setRunning(true);
		setError(undefined);
		try {
			setResult(await moveSession({ source, target: destination, onStep: setStep }));
		} catch (cause) {
			setError(cause instanceof MoveError ? cause : new MoveError(String(cause), step ?? "prepare", false));
		} finally {
			setRunning(false);
			refresh();
		}
	};
	const retryCommit = async () => {
		if (!result || !destination) return;
		setRunning(true);
		const commitError = await commitMove(source, { hostId: destination.hostId, label: result.destinationLabel }, result.transferId, result.sessionId);
		setResult({ ...result, committed: !commitError, commitError });
		setRunning(false);
		refresh();
	};
	const openMoved = () => {
		if (!result || !target) return;
		onClose();
		void navigate(sessionNavigateTarget(target.projectId, result.sessionId, target.hostId));
	};

	const stepState = (each: MoveStep): "done" | "active" | "todo" => {
		if (result) return each === "commit" && !result.committed ? "todo" : "done";
		if (!step) return "todo";
		const at = STEPS.indexOf(step);
		const index = STEPS.indexOf(each);
		return index < at ? "done" : index === at ? (running ? "active" : "todo") : "todo";
	};

	return (
		<Dialog open onOpenChange={(open) => !open && !running && onClose()}>
			<DialogContent className={settingsDialogContentClass} showCloseButton={!running}>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle>{t("moveSession.title")}</DialogTitle>
					<DialogDescription>{t("moveSession.description", { title: request.title })}</DialogDescription>
				</div>
				<div className={settingsDialogBodyClass}>
					{targets.length === 0 ? (
						<p className="text-sm text-muted-foreground">{t("moveSession.none")}</p>
					) : (
						<div role="radiogroup" aria-label={t("moveSession.pick")} className="flex flex-col gap-1">
							{targets.map((host) => (
								<button
									key={host.hostId}
									type="button"
									role="radio"
									aria-checked={host.hostId === target?.hostId}
									disabled={running || Boolean(result)}
									onClick={() => setTargetHost(host.hostId)}
									className="flex items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm hover:bg-interactive-hover aria-checked:bg-interactive-active disabled:opacity-60"
								>
									{host.hostId === target?.hostId ? <Check aria-hidden="true" className="size-icon-sm" /> : <Circle aria-hidden="true" className="size-icon-sm text-muted-foreground" />}
									<span className="min-w-0 truncate">{hostLabel(host.hostId)}</span>
									<span className="ml-auto truncate text-xs text-muted-foreground">{host.projectName}</span>
								</button>
							))}
						</div>
					)}
					{step || result ? (
						<ol className="flex flex-col gap-1.5 text-sm" aria-live="polite">
							{STEPS.map((each) => {
								const state = stepState(each);
								return (
									<li key={each} className={state === "todo" ? "flex items-center gap-2 text-muted-foreground" : "flex items-center gap-2"}>
										{state === "done" ? <Check aria-hidden="true" className="size-icon-sm" /> : state === "active" ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <Circle aria-hidden="true" className="size-icon-sm" />}
										{t(`moveSession.step.${each}`, { host: destination?.label ?? "" })}
									</li>
								);
							})}
						</ol>
					) : null}
					{error ? (
						<p role="alert" className="text-sm text-destructive">
							{t("moveSession.failed", { message: error.message })}{" "}
							{error.resumedSource ? t("moveSession.resumed", { host: source.label }) : t("moveSession.notResumed", { host: source.label })}
						</p>
					) : null}
					{result?.committed ? <p className="text-sm">{t("moveSession.done", { host: destination?.label ?? "" })}</p> : null}
					{result && !result.committed ? (
						<p role="alert" className="text-sm text-destructive">
							{t("moveSession.commitFailed", { target: destination?.label ?? "", source: source.label, message: result.commitError ?? "" })}
						</p>
					) : null}
					{result?.warnings.map((warning) => <p key={warning} className="text-xs text-muted-foreground">{warning}</p>)}
				</div>
				<div className={settingsDialogFooterClass}>
					{result ? (
						<>
							{!result.committed ? <Button variant="outline" disabled={running} onClick={() => void retryCommit()}>{t("moveSession.retry")}</Button> : null}
							<Button onClick={openMoved}>{t("moveSession.open")}</Button>
						</>
					) : (
						<>
							<Button variant="outline" disabled={running} onClick={onClose}>{t("moveSession.cancel")}</Button>
							<Button disabled={running || !destination} onClick={() => void start()}>{t("moveSession.start")}</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
