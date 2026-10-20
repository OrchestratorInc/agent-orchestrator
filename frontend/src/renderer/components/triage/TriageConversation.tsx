import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TriageRun } from "../../../shared/triage";
import type { AttachableTerminal } from "../../hooks/useTerminalSession";
import { aoBridge } from "../../lib/bridge";
import { sanitizeReportText } from "../../lib/report-problem";
import { triageConversationSnapshot, triagePendingActivity } from "../../lib/triage-conversation";
import { TriageTerminalStream } from "../../lib/triage-terminal";
import { isTriageActive } from "../../lib/triage";
import { useTriageStore } from "../../stores/triage-store";
import { useUiStore } from "../../stores/ui-store";
import { ChatWorkspace } from "../chat/ChatWorkspace";
import { XtermTerminal } from "../XtermTerminal";
import { Button } from "../ui/button";
import { TooltipProvider } from "../ui/tooltip";


export function TriageConversation({ runId }: { runId: string }) {
	const { t } = useTranslation();
	const run = useTriageStore((state) => state.runs.find((item) => item.id === runId));
	const error = useTriageStore((state) => state.runErrors[runId]);
	useEffect(() => { void useTriageStore.getState().loadRun(runId); }, [runId]);
	if (!run) return <p role={error ? "alert" : "status"} className="p-6 text-sm text-muted-foreground">{error ? sanitizeReportText(error) : t("triage.loading")}</p>;
	return run.transport === "terminal" ? <NativeTerminal key={run.id} run={run} /> : <NativeChat key={run.id} run={run} />;
}

function NativeChat({ run }: { run: TriageRun }) {
	const { t } = useTranslation();
	const busy = useTriageStore((state) => state.pending[run.id]);
	const error = useTriageStore((state) => state.runErrors[run.id]);
	const pendingInteraction = useMemo(() => triagePendingActivity(run), [run]);
	const snapshot = useMemo(() => triageConversationSnapshot(run), [run]);
	const resolve = (requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) =>
		useTriageStore.getState().reply(run.id, "", { requestId, action, ...(content ? { content } : {}) });
	const decide = (requestId: string, decisionId: string) => {
		void useTriageStore.getState().reply(run.id, "", { requestId, decisionId }).catch(() => {});
	};
	return <TooltipProvider><ChatWorkspace
		snapshot={snapshot} pendingInteraction={pendingInteraction} sessionTitle={t("triage.reportProblem")} inlineHeader
		busy={!!busy} commandError={sanitizeReportText(error || run.error || "") || undefined}
		headerActions={<div className="flex items-center gap-2"><span role="status" className="max-w-sm truncate text-xs text-muted-foreground">{sanitizeReportText(run.activity)}</span>
			{isTriageActive(run) ? <Button size="sm" variant="ghost" onClick={() => void useTriageStore.getState().cancel(run.id)}>{t("triage.stop")}</Button> : null}</div>}
		onSend={(text, attachments) => {
			if (attachments?.length) return Promise.reject(new Error("Reconnect AO to send files. Your message is still in the composer."));
			return useTriageStore.getState().reply(run.id, text);
		}}
		onDecide={decide}
		onResolveInput={resolve}
		onLinkOpen={(url) => { void aoBridge.app.openExternal(url); }}
	/></TooltipProvider>;
}

function NativeTerminal({ run }: { run: TriageRun }) {
	const { t } = useTranslation();
	const theme = useUiStore((state) => state.resolvedTheme);
	const stream = useMemo(() => new TriageTerminalStream(), [run.id]);
	const inputSubscription = useRef<{ dispose: () => void } | undefined>(undefined);
	const [error, setError] = useState<string>();

	useEffect(() => {
		let active = true;
		const unsubscribe = aoBridge.triage.subscribeTerminal(run.id, (data, sequence) => stream.push(data, sequence));
		void aoBridge.triage.get(run.id).then((snapshot) => {
			if (active) stream.snapshot(snapshot.terminalReplay ?? "", snapshot.terminalSequence ?? 0);
		}).catch((cause) => { if (active) setError(sanitizeReportText(cause instanceof Error ? cause.message : String(cause))); });
		return () => { active = false; unsubscribe(); inputSubscription.current?.dispose(); };
	}, [run.id, stream]);

	const onReady = (instance: AttachableTerminal) => {
		stream.attach((bytes) => instance.write(bytes));
		inputSubscription.current?.dispose();
		inputSubscription.current = instance.onUserInput((data) => {
			void aoBridge.triage.terminalInput(run.id, data).catch((cause) => setError(sanitizeReportText(cause instanceof Error ? cause.message : String(cause))));
		});
	};
	const resize = (cols: number, rows: number) => { void aoBridge.triage.terminalResize(run.id, rows, cols).catch((cause) => setError(sanitizeReportText(cause instanceof Error ? cause.message : String(cause)))); };
	const stop = () => { void useTriageStore.getState().cancel(run.id); };
	return <section className="flex h-full min-h-0 flex-col" aria-label={t("triage.reportProblem")}>
		<header className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-2">
			<p className="flex-1 truncate text-xs text-muted-foreground" role="status">{sanitizeReportText(run.activity || t("triage.title"))}</p>
			{isTriageActive(run) ? <Button size="sm" variant="ghost" onClick={stop}>{t("triage.stop")}</Button> : null}
		</header>
		{error ? <p role="alert" className="px-4 py-2 text-xs text-destructive">{error}</p> : null}
		<div className="min-h-0 flex-1"><XtermTerminal ariaLabel={t("triage.reportProblem")} theme={theme} onReady={onReady} onVisibleSize={resize} /></div>
	</section>;
}
