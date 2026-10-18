import { useNavigate } from "@tanstack/react-router";
import { Trans, useTranslation } from "react-i18next";
import { Clock3, History } from "lucide-react";
import { Button } from "./ui/button";
import { useAutomationRuns } from "../hooks/useAutomations";

export function displayTime(value?: string, locale?: string) {
	if (!value) return "—";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

/** One automation's runs, newest first, each linked to its session and, for a session-ended run, to the session that ended. */
export function AutomationRunHistory({ automationId, sessionTitles, title }: { automationId: string; sessionTitles: Map<string, string>; title?: string }) {
	const runs = useAutomationRuns(automationId);
	const navigate = useNavigate();
	const { t, i18n } = useTranslation();
	return <>
		<h3 className="mb-2 flex items-center gap-1.5 text-xs font-medium"><History className="size-3.5 text-muted-foreground" aria-hidden="true" />{title ?? t("automations.runs.title")}</h3>
		{runs.isLoading ? <p role="status" className="text-xs text-muted-foreground">{t("automations.runs.loading")}</p> : runs.error ? <p role="alert" className="text-xs text-destructive">{runs.error.message}</p> : runs.data?.length ? <div className="divide-y divide-border">{runs.data.map((run) => <div key={run.id} className="flex items-center justify-between gap-3 py-2 text-xs"><div><span className="font-medium capitalize">{run.status}</span><span className="ml-3 text-[11px] text-muted-foreground">{displayTime(run.scheduledFor, i18n.resolvedLanguage)}</span>{run.triggerSessionId ? <p className="mt-1 text-[11px] text-muted-foreground"><Trans i18nKey="automations.runs.trigger" values={{ session: sessionTitles.get(run.triggerSessionId) || run.triggerSessionId }} components={{ session: <button type="button" className="rounded-sm text-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => void navigate({ to: "/sessions/$sessionId", params: { sessionId: run.triggerSessionId! } })} /> }} /></p> : null}{run.errorMessage ? <p className="mt-1 text-xs text-destructive">{run.errorMessage}</p> : null}</div>{run.sessionId ? <Button variant="outline" size="sm" onClick={() => void navigate({ to: "/sessions/$sessionId", params: { sessionId: run.sessionId! } })}>{t("automations.runs.openSession")}</Button> : null}</div>)}</div> : <p className="flex items-center gap-2 py-3 text-xs text-muted-foreground"><Clock3 className="size-3.5" aria-hidden="true" />{t("automations.runs.empty")}</p>}
	</>;
}
