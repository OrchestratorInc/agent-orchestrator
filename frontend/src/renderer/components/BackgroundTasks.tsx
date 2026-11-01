import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ListTodo, RefreshCw } from "lucide-react";
import { useBackgroundTasks, type BackgroundTask, type BackgroundTaskReading } from "../hooks/useBackgroundTasks";
import { formatMemory, useDiagnosticsEnabled, useSessionMemory } from "../hooks/useSessionMemory";
import type { WorkspaceSession } from "../types/workspace";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";

function taskSummary(tasks: BackgroundTask[], t: TFunction): string | null {
	const live = tasks.filter((task) => task.state === "running");
	if (!live.length) return null;
	if (live.every((task) => task.kind === "shell")) return t("backgroundTasks.shells", { count: live.length });
	if (live.every((task) => task.kind === "agent")) return t("backgroundTasks.agents", { count: live.length });
	return t("backgroundTasks.tasks", { count: live.length });
}

export function supportsLocalBackgroundTasks(session: WorkspaceSession) {
	return session.mode === "chat" && !session.isTerminated && !session.hostId && !session.cloud;
}

/** The board supplies its shared batch reading; cards never create a poller. */
export function BackgroundTasksButton({ session, reading, stale = false }: {
	session: WorkspaceSession;
	reading?: BackgroundTaskReading;
	stale?: boolean;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const returnFocus = useRef<HTMLElement | null>(null);
	if (!supportsLocalBackgroundTasks(session)) return null;
	const summary = reading && reading.availability !== "unavailable" ? taskSummary(reading.tasks, t) : null;
	if (!summary && !open) return null;
	return (
		<>
			{summary ? <button
				type="button"
				className="inline-flex max-w-full items-center gap-1.5 rounded px-2 py-1 text-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground"
				onClick={(event) => {
					event.stopPropagation();
					// If the badge disappears after completion, return to the card's
					// primary button instead of leaving keyboard focus on the body.
					returnFocus.current = event.currentTarget.closest("[data-session-id]")?.querySelector<HTMLElement>("button") ?? event.currentTarget;
					setOpen(true);
				}}
			>
				<ListTodo aria-hidden="true" className="size-3" />
				<span>{summary}{stale ? ` · ${t("backgroundTasks.stale")}` : ""}</span>
			</button> : null}
			{open ? <BackgroundTasksDialog key={session.id} session={session} onOpenChange={setOpen} onReturnFocus={() => {
 if (returnFocus.current?.isConnected) returnFocus.current.focus();
 else document.querySelector<HTMLElement>('[aria-label="Message the agent"]')?.focus();
 }} /> : null}
		</>
	);
}

/** Chat/detail entry point. The board's badge uses the batch hook instead. */
export function SessionBackgroundTasks({ session }: { session: WorkspaceSession }) {
	const query = useBackgroundTasks(session.id, supportsLocalBackgroundTasks(session));
	return <BackgroundTasksButton session={session} reading={query.data} stale={query.isError} />;
}

function BackgroundTasksDialog({ session, onOpenChange, onReturnFocus }: { session: WorkspaceSession; onOpenChange: (open: boolean) => void; onReturnFocus: () => void }) {
	const { t } = useTranslation();
	const query = useBackgroundTasks(session.id, supportsLocalBackgroundTasks(session), true);
	const [selected, setSelected] = useState<BackgroundTask | null>(null);
	useEffect(() => setSelected(null), [query.data?.controllerGeneration]);
	const [processesOpen, setProcessesOpen] = useState(false);
	const diagnostics = useDiagnosticsEnabled();
	const ready = query.data && query.data.availability !== "unavailable";
	const tasks = query.data && query.data.availability !== "unavailable" ? query.data.tasks : [];
	const live = tasks.filter((task) => task.state === "running" || task.state === "paused");
	const recent = tasks.filter((task) => task.state !== "running" && task.state !== "paused");
	const current = selected && [...live, ...recent].find((task) => task.id === selected.id && task.runId === selected.runId);
	const detail = current ?? selected;
	return (
		<Dialog open onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[80vh] overflow-auto sm:max-w-xl" onClick={(event) => event.stopPropagation()} onCloseAutoFocus={(event) => { event.preventDefault(); onReturnFocus(); }}>
				<DialogTitle>{t("backgroundTasks.title")}</DialogTitle>
				<DialogDescription>{session.title}</DialogDescription>
				<div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
					<span>{query.data?.observedAt ? t("backgroundTasks.observed", { time: new Date(query.data.observedAt).toLocaleTimeString() }) : ""}</span>
					<button type="button" className="inline-flex items-center gap-1 rounded px-2 py-1 hover:bg-interactive-hover" onClick={() => void query.refetch()} disabled={query.isFetching}>
						<RefreshCw aria-hidden="true" className="size-3" />{t("backgroundTasks.refresh")}
					</button>
				</div>
				{query.isError ? <p role="status" className="text-xs text-warning">{t("backgroundTasks.staleReading")}</p> : null}
				{query.isPending ? <p className="text-sm text-muted-foreground">{t("backgroundTasks.loading")}</p> : !ready ? (
					<p className="text-sm text-muted-foreground">{t("backgroundTasks.unavailable")}</p>
				) : (
					<>
						{query.data?.availability === "partial" ? <p role="status" className="text-xs text-muted-foreground">{t("backgroundTasks.partial")}</p> : null}
						<h3 className="text-xs font-medium">{t("backgroundTasks.running", { count: live.length })}</h3>
						{!live.length ? <p className="text-sm text-muted-foreground">{t("backgroundTasks.empty")}</p> : null}
						{live.map((task) => <TaskRow key={`${task.id}:${task.runId ?? ""}`} task={task} onSelect={setSelected} />)}
						{recent.length ? <h3 className="mt-2 text-xs font-medium">{t("backgroundTasks.recent")}</h3> : null}
						{recent.map((task) => <TaskRow key={`${task.id}:${task.runId ?? ""}`} task={task} onSelect={setSelected} />)}
					</>
				)}
				{detail ? (
					<div className="rounded border border-border p-3 text-xs">
						<p className="mb-2 break-words font-medium">{detail.description || detail.id}</p>
						<p className="text-muted-foreground">{query.isError || !ready ? t("backgroundTasks.unavailable") : current ? t(`backgroundTasks.state.${detail.state}`) : t("backgroundTasks.noLongerReported")}</p>
						<p className="mt-2 break-all font-mono text-muted-foreground">{detail.id}</p>
					</div>
				) : null}
				<details className="border-t border-border pt-3 text-xs text-muted-foreground" onToggle={(event) => setProcessesOpen(event.currentTarget.open)}>
					<summary className="cursor-pointer">{t("backgroundTasks.processDetails")}</summary>
					<p className="my-2">{t("backgroundTasks.processHint")}</p>
					{processesOpen && diagnostics ? <SessionProcessDetails sessionId={session.id} /> : <p className="mt-2">{t("backgroundTasks.diagnosticsRequired")}</p>}
				</details>
			</DialogContent>
		</Dialog>
	);
}

function TaskRow({ task, onSelect }: { task: BackgroundTask; onSelect: (task: BackgroundTask) => void }) {
	const { t } = useTranslation();
	return <button type="button" aria-label={`${task.description || task.id} · ${t(`backgroundTasks.kind.${task.kind}`)} · ${t(`backgroundTasks.state.${task.state}`)}`} className="flex w-full items-start justify-between gap-3 rounded border border-border p-3 text-left text-xs hover:bg-interactive-hover" onClick={() => onSelect(task)}>
		<span className="min-w-0"><span className="block break-words">{task.description || task.id}</span><span className="mt-1 block text-muted-foreground">{t(`backgroundTasks.kind.${task.kind}`)}</span></span>
		<span className="shrink-0 text-muted-foreground">{t(`backgroundTasks.state.${task.state}`)}</span>
	</button>;
}

function SessionProcessDetails({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const query = useSessionMemory();
	const reading = query.data?.get(sessionId);
	if (query.isPending) return <p>{t("backgroundTasks.loading")}</p>;
	if (!reading || query.isError) return <p>{t("backgroundTasks.processUnavailable")}</p>;
	return <ul className="space-y-2">{reading.processes.map((process) => <li key={process.pid} className="flex justify-between gap-3 font-mono"><span className="min-w-0 truncate" title={process.command}>{process.pid} · {process.command}</span><span className="shrink-0">{formatMemory(process.rssBytes)}</span></li>)}</ul>;
}
