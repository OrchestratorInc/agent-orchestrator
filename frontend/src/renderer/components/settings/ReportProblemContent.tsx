import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { newChatDraftScope, readChatSessionDraft } from "../../lib/chat-drafts";
import { collectReportProblemDiagnostics, reportProblemDestinationUrl } from "../../lib/report-problem";
import { useShell } from "../../lib/shell-context";
import { captureRendererEvent } from "../../lib/telemetry";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { Button } from "../ui/button";

// A report never replaces text already typed on the new chat page.
const hasNewChatDraft = () => Boolean(readChatSessionDraft(newChatDraftScope()).composer.text.trim());

export function ReportProblemContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const { daemonStatus } = useShell();
	const [opening, setOpening] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [githubUrl, setGithubUrl] = useState("https://github.com/OrchestratorInc/agent-orchestrator/issues/new");
	useEffect(() => {
		if (!active) return;
		void captureRendererEvent("ao.renderer.support_opened");
		let cancelled = false;
		void collectReportProblemDiagnostics().then((diagnostics) => {
			if (!cancelled) setGithubUrl(reportProblemDestinationUrl(diagnostics));
		});
		return () => { cancelled = true; };
	}, [active]);
	const report = async () => {
		const destination = daemonStatus.state === "ready" ? "agent" : "terminal";
		let outcome: "succeeded" | "failed" = "failed";
		setOpening(true);
		setError(null);
		try {
			if (daemonStatus.state !== "ready") {
				const result = await aoBridge.triage.launch();
				outcome = result.ok ? "succeeded" : "failed";
				if (!result.ok) setError(t(result.error) + (result.command ? `\n${t("report.runCommand")}\n${result.command}` : ""));
				return;
			}
			if (hasNewChatDraft()) { setError(t("report.composerBusy")); return; }
			const result = await aoBridge.triage.prepare();
			if (!result.ok) { setError(t(result.error) + (result.command ? `\n${t("report.runCommand")}\n${result.command}` : "")); return; }
			const ui = useUiStore.getState();
			if (hasNewChatDraft()) { setError(t("report.composerBusy")); return; }
			ui.closeSettings();
			ui.requestNewTask(STANDALONE_WORKSPACE_ID, undefined, {
				initialPrompt: result.prompt, displayName: t("report.action"),
			});
			outcome = "succeeded";
		} catch {
			setError(t("report.failed"));
		} finally {
			setOpening(false);
			void captureRendererEvent("ao.renderer.support_submitted", { destination, outcome });
		}
	};
	return (
		<div className="flex flex-col items-start gap-3">
			<p className="text-sm text-muted-foreground">{t("report.description")}</p>
			<Button disabled={!active || opening} onClick={() => void report()}>
				{t(opening ? "report.opening" : "report.action")}
			</Button>
			{error ? <p role="alert" className="text-sm text-error">{error}</p> : null}
			<div className="flex flex-wrap gap-4 text-sm">
				{([
					["report.github", githubUrl, "github"],
					["report.discord", "https://discord.gg/WjKNa7EbB8", "discord"],
					["report.email", "mailto:prasad@untrivial.ai?cc=prateek@untrivial.ai", "email"],
				] as const).map(([label, href, destination]) => (
					<a key={label} href={href} className="text-muted-foreground underline-offset-2 hover:underline"
						onClick={(event) => {
							event.preventDefault();
							void aoBridge.app.openExternal(href).then(() => {
								void captureRendererEvent("ao.renderer.support_submitted", { destination, outcome: "succeeded" });
							}).catch(() => {
								setError(t("report.linkFailed"));
								void captureRendererEvent("ao.renderer.support_submitted", { destination, outcome: "failed" });
							});
						}}>{t(label)}</a>
				))}
			</div>
		</div>
	);
}
