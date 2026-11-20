import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TaskComposerEffortControl, TaskComposerModelControl } from "@aoagents/product-ui";
import { acceptsLocalModelID, agentModelDisplayLabel, isConcreteModelID, modelChoiceLabel } from "../lib/agent-model-choices";
import { agentLabel } from "../lib/agent-options";
import { fallbackEffort } from "../lib/effort";
import { listsModelVariants } from "../lib/model-variants";
import { AgentModelPicker, type AgentSwitchProps, type PickerSource } from "./chat/AgentSwitchPicker";
import { NativeModePicker } from "./NativeModePicker";
import { AgentModelCombobox } from "./settings/AgentModelCombobox";
import { EffortPicker, formatEffortLabel, type EffortAvailability } from "./settings/EffortPicker";
import { useModelTuning } from "./settings/ModelTuningControls";
import { MENU_TRIGGER_CHROME } from "./ui/option-menu";

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
	hostId,
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
	allowNativeSelection = false,
	footer,
	modelHint,
}: TaskComposerModelControl & {
	onRefresh?: () => Promise<void>;
	showFollowAgentAction: boolean;
	hostId?: string;
	/** Offer the native default as a choice and any model ID, as a cloud task can run them. */
	allowNativeSelection?: boolean;
	/** Where the listed models come from. */
	footer?: ReactNode;
	/** A quiet note beside one model's name, such as the one the orchestrator runs. */
	modelHint?: { id: string; label: string };
}) {
	const { t } = useTranslation();

	// Keep the no-agent placeholder until there is an agent to configure.
	if (agentId === "") {
		return (
			<span
				className={`${MENU_TRIGGER_CHROME} composer-chip composer-toolbar-option pointer-events-none w-fit cursor-not-allowed justify-start opacity-50`}
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
	// Open Interpreter's native catalog can be empty or partial for a custom
	// provider, so a local task still accepts a model ID it names directly.
	const directEntry = !allowNativeSelection && ((agentId === "open-interpreter" && catalog?.customModelEntry === "direct") || acceptsLocalModelID(agentId, catalog));
	const acceptsCustom = allowNativeSelection || directEntry;

	return (
		<AgentModelCombobox
			hostId={hostId}
			key={agentId}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			allowCustom={allowNativeSelection ? catalog?.allowCustom : directEntry}
			customModelEntry={allowNativeSelection ? catalog?.customModelEntry : directEntry ? "direct" : catalog?.customModelEntry === "configured" ? "configured" : "none"}
			onCustom={acceptsCustom ? onModelChange : undefined}
			// Default sends no model, so the project's or the agent's own applies.
			// A local direct-entry task needs it too, to undo a typed model ID.
			onUseDefault={allowNativeSelection || directEntry ? () => onModelChange("") : undefined}
			emptyLabel={allowNativeSelection ? t("newTask.cloudConfiguredDefault") : undefined}
			footer={footer}
			modelHint={modelHint}
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

/**
 * The automation form's agent, model and effort as the chat composer's one picker,
 * with selection state owned by the caller.
 */
export function TaskModelSelection(props: TaskComposerModelControl & {
	effort: string;
	onEffortChange: (value: string) => void;
	onValidityChange: (valid: boolean) => void;
	agentSwitch: AgentSwitchProps;
}) {
	const { t } = useTranslation();
	const { agentId, catalog, value, mode, effort, onEffortChange } = props;
	const models = catalog?.models;
	const selectedModel = value || models?.find((item) => item.isDefault)?.id || "";
	const { selected, invalidEffort } = useModelTuning({ models, model: selectedModel, effort, onEffortChange, onValidityChange: props.onValidityChange });
	const options = selected?.efforts?.filter((level) => level && level.toLowerCase() !== "default") ?? [];
	// An agent that picks its own model for each mode lists its modes as the models.
	const modes = catalog?.selectionMode === "mode";
	const defaultMode = modes ? models?.find((item) => item.isDefault && isConcreteModelID(item.id))?.id : undefined;
	// Cursor folds effort and thinking into its model ids; the picker splits them back out.
	const variants = listsModelVariants(agentId);
	const listed = (models ?? []).filter((item) => isConcreteModelID(item.id)).map((item) => ({
		id: item.id,
		label: item.id === "auto" ? t("settings.models.autoRouteLabel") : agentModelDisplayLabel(agentId, modes ? modelChoiceLabel(item) : item.label),
	}));
	const shownId = modes ? mode || defaultMode : selectedModel;
	const current: PickerSource = {
		models: listed,
		modelId: shownId,
		modelLabel: agentId === "" ? t("newTask.selectAgent") : listed.find((item) => item.id === shownId)?.label ?? agentLabel(agentId),
		onPickModel: modes
			? (next) => { props.onModeChange(next === defaultMode ? "" : next); props.onModelChange(""); onEffortChange(""); }
			: (next) => {
				const picked = models?.find((item) => next ? item.id === next : item.isDefault);
				props.onModelChange(next || picked?.id || "");
				props.onModeChange("");
				const levels = picked?.efforts ?? [];
				onEffortChange(effort && levels.includes(effort) ? effort : picked?.defaultEffort || fallbackEffort(levels) || "");
			},
		efforts: modes || variants ? [] : options.map((level) => ({ value: level, label: formatEffortLabel(level, t) })),
		// Unset follows the model's own default, which the picker names.
		effort: effort || fallbackEffort(options, selected?.defaultEffort) || selected?.defaultEffort || undefined,
		onPickEffort: (next) => {
			// An effort edit must stay attached to the model whose levels were offered.
			props.onModelChange(selectedModel);
			props.onModeChange("");
			onEffortChange(next || selected?.defaultEffort || "");
		},
	};
	return <>
		<AgentModelPicker currentHarness={agentId} current={current} agentSwitch={props.agentSwitch} disabled={props.disabled} loadingModels={props.fetching && !catalog} />
		{invalidEffort ? <p role="alert" className="text-xs text-warning">{t("settings.models.unsupportedTuning", { role: "" })}</p> : null}
	</>;
}
