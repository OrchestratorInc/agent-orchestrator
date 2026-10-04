import { useEffect, useState } from "react";
import { Loader2, TriangleAlert } from "lucide-react";
import type { components } from "../../api/schema";
import { useUiStore } from "../stores/ui-store";

export function StartupCueBanner({ sessionId, run }: {
	sessionId: string;
	run?: components["schemas"]["StartupCueRun"];
}) {
	const [now, setNow] = useState(Date.now);
	const running = run?.state === "pending" || run?.state === "running";
	const title = run?.state === "cancelled" ? "Startup cue interrupted." : "Startup cue failed; session continued.";
	const failed = run?.state === "failed" || run?.state === "interrupted" || run?.state === "cancelled";
	useEffect(() => {
		if (!running) return;
		const timer = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(timer);
	}, [running]);
	useEffect(() => {
		if (!failed || !run) return;
		useUiStore.getState().showGlobalToast(title.replace(/\.$/, ""), run.error ?? run.name, {
			tone: "error", dedupeKey: `startup-cue:${sessionId}:${run.startedAt}`,
		});
	}, [failed, run?.error, run?.startedAt, sessionId, title]);
	if (!run || (!running && !failed)) return null;
	const elapsed = Math.max(0, Math.floor((now - Date.parse(run.startedAt)) / 1000));
	return <div role={failed ? "alert" : "status"} className="mx-4 mb-2 flex items-start gap-2 rounded-md border border-border px-3 py-2 text-xs">
		{running ? <Loader2 aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 animate-spin" /> : <TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />}
		<div className="min-w-0 flex-1">
			<p className="font-medium">{running ? `Running startup cue: ${run.name}… (${elapsed}s)` : title}</p>
			<p className="mt-1 text-muted-foreground">{running ? "Preparing the worktree. Messages are queued until the command finishes." : run.error}</p>
			{failed ? <details className="mt-2">
				<summary className="cursor-pointer">Command and output: {run.name}</summary>
				<pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words">{run.command}{"\n\n"}{run.output || "No command output."}</pre>
			</details> : null}
		</div>
	</div>;
}
