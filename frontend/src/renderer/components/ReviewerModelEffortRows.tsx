import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions, type AgentModelCatalog } from "../hooks/useAgentModelsQuery";
import { isConcreteModelID } from "../lib/agent-model-choices";
import { AgentModelField } from "./settings/ProjectAgentRoleControls";
import { EffortPicker } from "./settings/EffortPicker";
import { OptionMenuItem } from "./ui/option-menu";

export type ReviewerModelConfig = { model?: string; mode?: string; effort?: string };

// The review controls are one grouped list: a bordered box whose rows are split
// by dividers. Every row has the same height and every value the same type and
// button shape, so agent, model, and effort line up however long their names are.
export const REVIEWER_GROUP = "min-w-0 divide-y divide-border overflow-hidden rounded-md border border-border";
export const REVIEWER_ROW = "flex h-9 min-w-0 items-center gap-3 pl-3 pr-1";
export const REVIEWER_ROW_LABEL = "shrink-0 text-sm text-settings-label";
/** The value side of a row: fills the rest of it, right-aligned, truncating a long name. */
export const REVIEWER_ROW_TRIGGER = "h-control-sm min-w-0 w-full flex-1 justify-end gap-1.5 rounded-md border-0 bg-transparent px-2 text-right text-xs text-foreground shadow-none";
const ROW = REVIEWER_ROW;
const LABEL = REVIEWER_ROW_LABEL;

/**
 * The reviewer's model and effort, each on its own row under the agent picker,
 * built from the same pieces as Project Settings. Every pick reports the whole
 * config, so the caller saves model, mode, and effort together.
 */
export function ReviewerModelEffortRows({
	agentId,
	projectId,
	hostId,
	model,
	mode,
	effort,
	inherited = {},
	disabled = false,
	onChange,
}: {
	agentId: string;
	projectId: string;
	hostId?: string;
	/** The session's own reviewer settings; empty means "inherit". */
	model: string;
	mode: string;
	effort: string;
	/** What an empty session field inherits from the project's reviewer for this agent. */
	inherited?: ReviewerModelConfig;
	disabled?: boolean;
	onChange: (config: ReviewerModelConfig) => void;
}) {
	const { t } = useTranslation();
	const catalog = useQuery(agentModelsQueryOptions(agentId, projectId, hostId)).data;
	// A model pick arrives as two calls (model, then mode). Gather the calls from
	// one pick and save once, so the second does not overwrite the first.
	const pending = useRef<{ model: string; mode: string; effort: string } | null>(null);
	const update = (patch: Partial<{ model: string; mode: string; effort: string }>) => {
		const flush = pending.current === null;
		pending.current = { ...(pending.current ?? { model, mode, effort }), ...patch };
		if (!flush) return;
		queueMicrotask(() => {
			const next = pending.current;
			pending.current = null;
			if (!next) return;
			onChange({
				...(next.model ? { model: next.model } : {}),
				...(next.mode ? { mode: next.mode } : {}),
				...(next.effort ? { effort: next.effort } : {}),
			});
		});
	};
	// Switching model keeps the chosen effort only when the new model offers it.
	const keptEffort = (id: string) => (effort && catalogModel(catalog, id || defaultModelID(catalog))?.efforts?.includes(effort) ? effort : "");

	// The daemon fills an empty session field from the project's reviewer for this
	// agent, so show what the reviewer will actually run with.
	const shownModel = model || (mode ? "" : inherited.model ?? "");
	const shownMode = mode || (model ? "" : inherited.mode ?? "");
	const inheritedEffort = inherited.effort ?? "";
	const shownEffort = effort || inheritedEffort;

	// Effort belongs to the model the reviewer runs with: the chosen one, or the catalog default.
	const effortModel = catalogModel(catalog, shownMode || shownModel || defaultModelID(catalog));
	const choices = (effortModel?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default").map((value) => ({ value }));
	const reportedDefault = effortModel?.defaultEffort?.trim().toLowerCase();
	// Saving an empty effort means "inherit". With nothing inherited that is the
	// model's default, so picking the reported default clears the override. When
	// the project sets an effort, every level is saved as picked, or the model's
	// default could never be chosen; Clear effort then goes back to the project's.
	const needsClear = effort !== "" && (inheritedEffort !== ""
		|| (choices.some((choice) => choice.value === effort) && !choices.some((choice) => choice.value.toLowerCase() === reportedDefault)));

	return (
		<>
			{/* A catalog warning wraps onto its own line under the row. */}
			<div className={`${ROW} h-auto min-h-9 flex-wrap`}>
				<span className={LABEL}>{t("settings.models.reviewerModel")}</span>
				<div className="flex min-w-0 flex-1 flex-wrap justify-end">
					<AgentModelField
						triggerClassName={REVIEWER_ROW_TRIGGER}
						menuAlign="end"
						role="reviewer"
						agentId={agentId}
						projectId={projectId}
						hostId={hostId}
						model={shownModel}
						mode={shownMode}
						effort={shownEffort}
						showEffort={false}
						disabled={disabled}
						onModelChange={(value) => update({ model: value, effort: keptEffort(value) })}
						onModeChange={(value) => update({ mode: value, ...(value ? { effort: keptEffort(value) } : {}) })}
						onEffortChange={() => undefined}
						onValidityChange={() => undefined}
						warningClassName="basis-full pb-1 text-right"
					/>
				</div>
			</div>
			{choices.length > 0 || shownEffort ? (
				<div className={ROW}>
					<span className={LABEL}>{t("settings.models.effort")}</span>
					<EffortPicker
						label={t("settings.models.effort")}
						value={shownEffort}
						defaultValue={inheritedEffort ? null : ""}
						choices={choices}
						defaultEffort={effortModel?.defaultEffort}
						availability={effortModel?.efforts === undefined ? "unknown" : choices.length > 0 ? "supported" : "unsupported"}
						followLabel={t("agentSwitch.defaultModel")}
						disabled={disabled}
						triggerVariant="ghost"
						triggerClassName={REVIEWER_ROW_TRIGGER}
						onChange={(value) => update({ effort: value })}
						footer={needsClear ? <OptionMenuItem onSelect={() => update({ effort: "" })}>{t("settings.models.clearEffort")}</OptionMenuItem> : undefined}
					/>
				</div>
			) : null}
		</>
	);
}

function defaultModelID(catalog: AgentModelCatalog | undefined): string {
	return catalog?.models?.find((item) => item.isDefault && isConcreteModelID(item.id))?.id ?? "";
}

function catalogModel(catalog: AgentModelCatalog | undefined, id: string) {
	return id ? catalog?.models?.find((item) => item.id === id) : undefined;
}
