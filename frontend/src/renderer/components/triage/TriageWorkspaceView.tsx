import { useTranslation } from "react-i18next";
import type { CSSProperties } from "react";
import { isMacPlatform } from "../../lib/platform";
import { WindowTitlebar } from "../WindowTitlebar";
import { TooltipProvider } from "../ui/tooltip";
import { TriageConversation } from "./TriageConversation";
import { TriageLauncher } from "./TriageLauncher";

export function TriageWorkspaceView({ runId, recovery = false }: { runId?: string; recovery?: boolean }) {
	const { t } = useTranslation();
	return <section className="flex h-full min-h-0 flex-col overflow-hidden bg-background text-foreground" data-testid={recovery ? "triage-recovery-view" : "triage-workspace-view"}>
		{recovery ? <>
			<TooltipProvider><WindowTitlebar /></TooltipProvider>
			{isMacPlatform() ? <div aria-hidden="true" className="h-traffic-light-clearance shrink-0" style={{ WebkitAppRegion: "drag" } as CSSProperties} /> : null}
			<div className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-2 text-xs text-muted-foreground"><p className="flex-1">{t("triage.recovery")}</p><a href="#/">{t("triage.back")}</a></div>
		</> : null}
		<div className="min-h-0 flex-1">{runId ? <TriageConversation runId={runId} /> : <div className="p-6"><TriageLauncher /></div>}</div>
	</section>;
}
