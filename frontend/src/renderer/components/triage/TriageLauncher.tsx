import { useEffect, useRef, useState } from "react";
import { queryClient } from "../../lib/query-client";
import { useTranslation } from "react-i18next";
import { ChevronDown, Loader2 } from "lucide-react";
import { useTriageStore } from "../../stores/triage-store";
import { cachedTriageOrigin, configuredTriageSnapshot, currentTriageOrigin, defaultTriageSelection, triageHref, validTriageSelection } from "../../lib/triage";
import { sanitizeReportText } from "../../lib/report-problem";
import type { TriageOrigin, TriageSelection } from "../../../shared/triage";
import { AgentModelCombobox } from "../settings/AgentModelCombobox";
import { AgentSelectMenuItem } from "../settings/AgentSelectMenuItem";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../ui/dialog";
import { useUiStore } from "../../stores/ui-store";

export function TriageLauncher({ active = true, origin }: { active?: boolean; origin?: TriageOrigin }) {
	const { t } = useTranslation();
	const state = useTriageStore();
	const [open, setOpen] = useState(false);
	const openRef = useRef(false);
	const preparationGeneration = useRef<number | null>(null);
	const choice = state.choices.find((item) => item.harness === state.selection?.harness);
	const preparation = state.runs.find((run) => run.id === state.preparationId);
	const uncertain = preparation?.execution === "session" && !preparation.sessionId;
	const discardOwnPreparation = () => {
		const current = useTriageStore.getState();
		if (!current.starting && preparationGeneration.current === current.preparationGeneration) current.discardPreparation();
	};
	const close = (value: boolean) => {
		openRef.current = value;
		setOpen(value);
		if (!value) discardOwnPreparation();
	};
	useEffect(() => () => { if (openRef.current) discardOwnPreparation(); }, []);

	const prepare = async () => {
		close(true);
		const context = cachedTriageOrigin(queryClient, origin ?? currentTriageOrigin());
		state.setContext(context);
		void state.prepare(context);
		preparationGeneration.current = useTriageStore.getState().preparationGeneration;
		await state.loadCatalog((native) => configuredTriageSnapshot(queryClient, native));
		if (!openRef.current || preparationGeneration.current !== useTriageStore.getState().preparationGeneration) return;
		const current = useTriageStore.getState();
		const existing = current.runs.find((run) => run.execution === "session" && !run.sessionId);
		const selection = existing?.request ?? defaultTriageSelection(queryClient, current.choices, context);
		if (selection) useTriageStore.setState({ selection });
		else if (current.catalogLoaded) useTriageStore.setState({ error: current.error || t("triage.empty") });
	};

	const changeSelection = (selection: TriageSelection) => {
		state.setSelection(selection);
		useTriageStore.setState({ error: null });
	};
	const start = async () => {
		if (state.starting) return;
		try {
			const current = useTriageStore.getState();
			const selection = current.selection;
			const pendingSession = current.runs.find((run) => run.id === current.preparationId && run.execution === "session" && !run.sessionId);
			if (!validTriageSelection(current.choices, selection) && !pendingSession) {
				throw new Error(choice?.unavailableReason || t(selection ? "triage.defaultUnavailable" : "triage.empty"));
			}
			const run = await current.start();
			if (!run) return;
			if (run.execution === "session" && !run.sessionId) {
				useTriageStore.setState({ error: run.error || "AO could not confirm whether the session started. Start again to check the same launch." });
				return;
			}
			close(false);
			useUiStore.setState({ settingsModal: null });
			if (run.sessionId) {
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
				const target = sessionNavigateTarget(undefined, run.sessionId);
				window.location.hash = target.to.replace("$sessionId", encodeURIComponent(target.params.sessionId));
			} else window.location.hash = triageHref(run.id).slice(1);
		} catch (error) { useTriageStore.setState({ error: error instanceof Error ? error.message : String(error) }); }
		finally { if (!openRef.current) discardOwnPreparation(); }
	};

	return <>
		<Button size="sm" disabled={!active || state.starting} onClick={() => void prepare()}>{t("triage.reportProblem")}</Button>
		<Dialog open={open} onOpenChange={close}>
			<DialogContent className="max-w-md" data-testid="triage-launcher">
				<DialogHeader>
					<DialogTitle>{t("triage.reportProblem")}</DialogTitle>
					<DialogDescription>{t("triage.launcherIntro")}</DialogDescription>
				</DialogHeader>
				<div className="flex flex-wrap items-center gap-2" aria-busy={state.loading}>
					<DropdownMenu>
						<DropdownMenuTrigger asChild><Button variant="outline" size="sm" disabled={state.loading || state.starting || uncertain || !state.choices.length} aria-label={t("triage.agent")}>
							{choice?.label ?? state.selection?.harness ?? t("triage.chooseAgent")}<ChevronDown className="size-3" aria-hidden="true" />
						</Button></DropdownMenuTrigger>
						<DropdownMenuContent align="start" className="max-w-sm">
							{state.choices.map((item) => <DropdownMenuItem key={item.harness} className="flex-col items-stretch gap-1"
								onSelect={() => changeSelection(defaultTriageSelection(queryClient, state.choices, state.origin, item.harness) ?? { harness: item.harness })}>
								<AgentSelectMenuItem agentId={item.harness} label={item.label} selected={item.harness === state.selection?.harness} />
								{!item.available ? <span className="line-clamp-2 whitespace-normal break-words pl-8 text-caption leading-snug text-muted-foreground" title={sanitizeReportText(item.unavailableReason || t("triage.unavailable"))}>{sanitizeReportText(item.unavailableReason || t("triage.unavailable"))}</span> : null}
							</DropdownMenuItem>)}
						</DropdownMenuContent>
					</DropdownMenu>
					{choice?.models.length ? <AgentModelCombobox aria-label={t("triage.model")} compact menuAlign="start"
						value={state.selection?.model ?? ""} models={choice.models.map((item) => ({ ...item, isDefault: item.default }))}
						emptyLabel={t("triage.chooseModel")} disabled={state.loading || state.starting || uncertain}
						onChange={(model) => changeSelection({ ...state.selection!, model: model || choice.models.find((item) => item.default)?.id || "", effort: undefined })} /> : null}
				</div>
				{state.preparing || preparation ? <p role="status" className="text-xs text-muted-foreground">{preparation ? sanitizeReportText(preparation.activity) : t("triage.collectingInitial")}</p> : null}
				{state.preparationError ? <p className="text-xs text-destructive">{sanitizeReportText(state.preparationError)}</p> : null}
				<p className="text-xs leading-relaxed text-muted-foreground">{t("triage.disclosure")}</p>
				{!state.loading && state.catalogLoaded && !state.choices.length && !state.error ? <p className="text-xs text-muted-foreground">{t("triage.empty")}</p> : null}
				{state.error ? <p role="alert" className="text-xs text-destructive">{sanitizeReportText(state.error)}</p> : null}
				<div className="flex justify-end"><Button size="sm" disabled={state.loading || state.starting || (!state.selection && !uncertain)} onClick={() => void start()}>
					{state.starting ? <Loader2 className="size-3 animate-spin" aria-hidden="true" /> : null}{t("triage.start")}
				</Button></div>
			</DialogContent>
		</Dialog>
	</>;
}
