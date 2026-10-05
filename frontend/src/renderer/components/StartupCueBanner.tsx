import { useEffect, useState } from "react";
import { Loader2, TriangleAlert, X } from "lucide-react";
import type { components } from "../../api/schema";
import { useUiStore } from "../stores/ui-store";
import { apiClient } from "../lib/api-client";

const dismissedStartupRuns = new Set<string>();

export function StartupCueBanner({ sessionId, run }: {
	sessionId: string;
	run?: components["schemas"]["StartupCueRun"];
}) {
	const [now, setNow] = useState(Date.now);
	const running = run?.state === "pending" || run?.state === "running";
	const title = run?.state === "cancelled" ? "Startup cue interrupted." : "Startup cue failed; session continued.";
	const failed = run?.state === "failed" || run?.state === "interrupted" || run?.state === "cancelled";
	const toastKey = run ? `startup-cue:${sessionId}:${run.startedAt}` : "";
	const runKey = run ? `${sessionId}:${run.startedAt}` : "";
	const [dismissed, setDismissed] = useState(() => Boolean(runKey && dismissedStartupRuns.has(runKey)));
	const [cancelling, setCancelling] = useState(false);
	useEffect(() => { setDismissed(Boolean(runKey && dismissedStartupRuns.has(runKey))); }, [runKey]);
	useEffect(() => {
		if (!running) return;
		const timer = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(timer);
	}, [running]);
	useEffect(() => {
		if (!failed || !run || dismissed) return;
		useUiStore.getState().showGlobalToast(title.replace(/\.$/, ""), run.error ?? run.name, {
			tone: "error", dedupeKey: `startup-cue:${sessionId}:${run.startedAt}`,
		});
	}, [dismissed, failed, run?.error, run?.startedAt, sessionId, title]);
	useEffect(() => {
		if (dismissed && toastKey) {
			const toast = useUiStore.getState().globalToasts.find((item) => item.dedupeKey === toastKey);
			if (toast) useUiStore.getState().dismissGlobalToast(toast.nonce);
		}
	}, [dismissed, toastKey]);
	if (!run || (!running && failed && dismissed) || (!running && !failed)) return null;
	const elapsed = Math.max(0, Math.floor((now - Date.parse(run.startedAt)) / 1000));
	return <div role={failed ? "alert" : "status"} className="relative mx-4 mb-2 flex items-start gap-2 rounded-md border border-border px-3 py-2 text-xs">
		{running ? <Loader2 aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 animate-spin" /> : <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />}
		<div className="min-w-0 flex-1">
			<p className="font-medium">{running ? `Running startup cue: ${run.name}… (${elapsed}s)` : title}</p>
			{running && run.terminalHandle ? <p className="mt-1 text-muted-foreground">Terminal: {run.terminalHandle}</p> : null}
			<p className="mt-1 text-muted-foreground">{running ? "Preparing the worktree. Messages are queued until the command finishes." : run.error}</p>
			{running ? <button type="button" disabled={cancelling} className="mt-2 rounded border border-border px-2 py-1 text-xs hover:bg-muted" onClick={async () => {
				setCancelling(true);
				try { await apiClient.POST("/api/v1/sessions/{sessionId}/startup-cue/cancel", { params: { path: { sessionId } } }); } finally { setCancelling(false); }
			}}>{cancelling ? "Cancelling…" : "Cancel setup"}</button> : null}
			{failed ? <>
				<button type="button" aria-label="Dismiss startup cue error" className="absolute right-2 top-2 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => {
					dismissedStartupRuns.add(runKey);
					setDismissed(true);
					const toast = useUiStore.getState().globalToasts.find((item) => item.dedupeKey === toastKey);
					if (toast) useUiStore.getState().dismissGlobalToast(toast.nonce);
				}}><X aria-hidden="true" className="size-3.5" /></button>
				<details className="mt-2">
				<summary className="cursor-pointer">Command and output: {run.name}</summary>
				<pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words">{run.command}{"\n\n"}{run.output || "No command output."}</pre>
				</details></> : null}
		</div>
	</div>;
}
