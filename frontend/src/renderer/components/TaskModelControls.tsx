import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TaskComposerEffortControl, TaskComposerModelControl } from "@aoagents/product-ui";
import { agentModelDisplayLabel, isConcreteModelID } from "../lib/agent-model-choices";
import { fallbackEffort } from "../lib/effort";
import { collapseModelVariants, findModelVariant, listsModelVariants, pickModelVariant } from "../lib/model-variants";
import { ToggleRow } from "./chat/AgentSwitchPicker";
import { NativeModePicker } from "./NativeModePicker";
import { AgentModelCombobox } from "./settings/AgentModelCombobox";
import { EffortPicker, type EffortAvailability } from "./settings/EffortPicker";
import { useModelTuning } from "./settings/ModelTuningControls";

// The agent, model and effort menus share one compact width.
export const COMPOSER_MENU_WIDTH = "w-[min(14rem,calc(100vw-2rem))]! min-w-0! max-w-[calc(100vw-2rem)]!";

export function TaskEffortPicker({
	disabled,
	label,
	onChange,
	options,
	value,
	defaultEffort,
	availability,
	footer,
}: TaskComposerEffortControl & { defaultEffort?: string; availability: EffortAvailability; footer?: ReactNode }) {
	return (
		<EffortPicker
			label={label}
			disabled={disabled}
			value={value.toLowerCase() === "default" ? "" : value}
			choices={options.map((option) => ({ value: option }))}
			defaultEffort={defaultEffort}
			availability={availability}
			onChange={onChange}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuClassName={COMPOSER_MENU_WIDTH}
			footer={footer}
		/>
	);
}

export function TaskModelPicker({
	agentId,
	agentLabel,
	catalog,
	disabled,
	value,
	mode,
	onModelChange,
	onModeChange,
	onRefresh,
	showFollowAgentAction,
}: TaskComposerModelControl & { onRefresh: () => Promise<void>; showFollowAgentAction: boolean }) {
	const { t } = useTranslation();

	// Keep the no-agent placeholder until there is an agent to configure.
	if (agentId === "") {
		return (
			<span
				className="composer-chip composer-toolbar-option w-fit cursor-not-allowed justify-start opacity-50"
				aria-disabled="true"
				aria-label={t("newTask.model")}
			>
				<span className="truncate text-settings-muted">{t("newTask.selectAgent")}</span>
			</span>
		);
	}

	if (catalog?.selectionMode === "mode") {
		return (
			<NativeModePicker
				models={catalog.models ?? []}
				mode={mode}
				onModeChange={onModeChange}
				disabled={disabled}
				showFollowAgentAction={showFollowAgentAction}
				triggerClassName="composer-chip composer-toolbar-option w-fit"
				menuClassName={COMPOSER_MENU_WIDTH}
			/>
		);
	}

	const displayModels = (catalog?.models ?? []).map((item) => {
		if (item.id === "auto") return { ...item, label: t("settings.models.autoRouteLabel") };
		return { ...item, label: agentModelDisplayLabel(agentId, item.label) };
	});
	const selectCatalogModel = (nextModel: string) => {
		onModelChange(nextModel);
	};

	return (
		<AgentModelCombobox
			key={agentId}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			allowCustom={false}
			customModelEntry={catalog?.customModelEntry === "configured" ? "configured" : "none"}
			agentLabel={agentLabel}
			onRefresh={onRefresh}
			refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
			refreshError={catalog?.refreshError}
			retryAt={catalog?.retryAt}
			disabled={disabled || agentId === ""}
			agentId={agentId}
			onChange={selectCatalogModel}
			compact
			recentScope={agentId}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuAlign="start"
			menuClassName={COMPOSER_MENU_WIDTH}
			renderTrigger={(label) => <span className="min-w-0 truncate text-control text-foreground" title={label}>{label}</span>}
		/>
	);
}

/** New Task's model and effort controls, with selection state owned by the caller. */
export function TaskModelSelection(props: TaskComposerModelControl & {
	effort: string;
	onEffortChange: (value: string) => void;
	onValidityChange: (valid: boolean) => void;
	onRefresh: () => Promise<void>;
}) {
	const { t } = useTranslation();
	const { catalog, value, effort, onEffortChange } = props;
	const models = catalog?.models;
	const selectedModel = value || models?.find((item) => item.isDefault)?.id || "";
	const { selected, invalidEffort } = useModelTuning({ models, model: selectedModel, effort, onEffortChange, onValidityChange: props.onValidityChange });
	const options = selected?.efforts?.filter((level) => level && level.toLowerCase() !== "default") ?? [];
	const families = listsModelVariants(props.agentId) ? collapseModelVariants((models ?? []).filter((item) => isConcreteModelID(item.id))) : undefined;
	const cursorChoice = families ? findModelVariant(families, selectedModel) : undefined;
	const pickVariant = (want: { effort?: string; thinking?: boolean }) => {
		if (cursorChoice) {
			props.onModelChange(pickModelVariant(cursorChoice.family, { ...cursorChoice.variant, ...want }).id);
			onEffortChange("");
		}
	};
	const availability: EffortAvailability = !selected || selected.efforts === undefined ? "unknown" : options.length ? "supported" : "unsupported";
	return <>
		<div className="flex min-w-0 flex-wrap items-center gap-1.5">
			<TaskModelPicker {...props} value={selectedModel} showFollowAgentAction={true}
				onModelChange={(next) => {
					const picked = models?.find((item) => next ? item.id === next : item.isDefault);
					props.onModelChange(next || picked?.id || "");
					props.onModeChange("");
					const levels = picked?.efforts ?? [];
					onEffortChange(effort && levels.includes(effort) ? effort : picked?.defaultEffort || fallbackEffort(levels) || "");
				}}
				onModeChange={(next) => { props.onModeChange(next); props.onModelChange(""); onEffortChange(""); }}
			/>
			<TaskEffortPicker id={`${props.id}-effort`} label={t("settings.models.effort")} disabled={props.disabled}
				options={cursorChoice ? cursorChoice.family.efforts : options}
				value={cursorChoice ? cursorChoice.variant.effort ?? "" : effort || fallbackEffort(options, selected?.defaultEffort) || ""}
				defaultEffort={cursorChoice ? undefined : selected?.defaultEffort}
				availability={cursorChoice ? "supported" : availability}
				onChange={cursorChoice ? (next) => pickVariant({ effort: next }) : (next) => {
					// An effort edit must stay attached to the model whose levels were offered.
					props.onModelChange(selectedModel);
					props.onModeChange("");
					onEffortChange(next || selected?.defaultEffort || "");
				}}
				footer={cursorChoice?.family.hasThinkingChoice ? <ToggleRow label={t("agentSwitch.thinking")} checked={cursorChoice.variant.thinking} onChange={(thinking) => pickVariant({ thinking })} /> : undefined}
			/>
		</div>
		{invalidEffort ? <p role="alert" className="text-xs text-warning">{t("settings.models.unsupportedTuning", { role: "" })}</p> : null}
	</>;
}
