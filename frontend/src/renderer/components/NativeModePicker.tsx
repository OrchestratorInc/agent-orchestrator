import { useTranslation } from "react-i18next";
import { isConcreteModelID, modelChoiceLabel } from "../lib/agent-model-choices";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type NativeModePickerProps = {
	models: ReadonlyArray<{ id: string; label: string; isDefault?: boolean }>;
	mode: string;
	disabled?: boolean;
	showFollowAgentAction?: boolean;
	triggerClassName: string;
	menuClassName?: string;
	onModeChange: (value: string) => void;
};

export function NativeModePicker({
	models,
	mode,
	disabled = false,
	showFollowAgentAction = true,
	triggerClassName,
	menuClassName,
	onModeChange,
}: NativeModePickerProps) {
	const { t } = useTranslation();
	const options = models.filter((item) => isConcreteModelID(item.id)).map((item) => ({
		value: item.id,
		label: modelChoiceLabel(item),
	}));
	const explicitMode = isConcreteModelID(mode) ? mode : "";
	const defaultMode = models.find((item) => item.isDefault && isConcreteModelID(item.id))?.id || "";
	const effectiveMode = explicitMode || defaultMode;
	const visibleModeLabel = options.find((option) => option.value === effectiveMode)?.label ?? (explicitMode || t("settings.models.modeNotReported"));
	const action = explicitMode && !defaultMode && showFollowAgentAction
		? { label: t("settings.models.useAgentMode"), onSelect: () => onModeChange("") }
		: undefined;
	return (
		<SettingsOptionMenu
			aria-label={t("newTask.model")}
			value={effectiveMode}
			options={options}
			action={action}
			disabled={disabled || (options.length === 0 && !action)}
			triggerClassName={triggerClassName}
			menuAlign="start"
			menuClassName={menuClassName}
			renderTrigger={() => (
				<span className="min-w-0 truncate text-control text-foreground" title={visibleModeLabel}>
					{visibleModeLabel}
				</span>
			)}
			onChange={(value) => onModeChange(value === defaultMode ? "" : value)}
		/>
	);
}
