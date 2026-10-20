import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { collectReportProblemDiagnostics, reportProblemDestinationUrl } from "../../lib/report-problem";
import { useShell } from "../../lib/shell-context";
import { useUiStore } from "../../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { Button } from "../ui/button";

export function ReportProblemContent({ active }: { active: boolean }) {
	const { t } = useTranslation();
	const { daemonStatus } = useShell();
	const [opening, setOpening] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [githubUrl, setGithubUrl] = useState("https://github.com/OrchestratorInc/agent-orchestrator/issues/new");
	useEffect(() => {
		if (!active) return;
		let cancelled = false;
		void collectReportProblemDiagnostics().then((diagnostics) => {
			if (!cancelled) setGithubUrl(reportProblemDestinationUrl(diagnostics));
		});
		return () => { cancelled = true; };
	}, [active]);
	const report = async () => {
		setOpening(true);
		setError(null);
		try {
			if (daemonStatus.state !== "ready") {
				const result = await aoBridge.triage.launch();
				if (!result.ok) setError(result.error);
				return;
			}
			const result = await aoBridge.triage.prepare();
			if (!result.ok) { setError(result.error); return; }
			const ui = useUiStore.getState();
			ui.closeSettings();
			ui.requestNewTask(STANDALONE_WORKSPACE_ID, undefined, {
				initialPrompt: result.prompt, displayName: t("report.action"),
			});
		} catch (error) {
			setError(error instanceof Error ? error.message : t("report.failed"));
		} finally { setOpening(false); }
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
					["report.github", githubUrl],
					["report.discord", "https://discord.gg/WjKNa7EbB8"],
					["report.email", "mailto:prasad@untrivial.ai?cc=prateek@untrivial.ai"],
				] as const).map(([label, href]) => (
					<a key={label} href={href} className="text-muted-foreground underline-offset-2 hover:underline"
						onClick={(event) => {
							event.preventDefault();
							void aoBridge.app.openExternal(href).catch(() => setError(t("report.linkFailed")));
						}}>{t(label)}</a>
				))}
			</div>
		</div>
	);
}
