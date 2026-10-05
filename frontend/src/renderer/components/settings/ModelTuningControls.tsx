import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { isConcreteModelID } from "../../lib/agent-model-choices";
import { EffortPicker } from "./EffortPicker";
import { SettingsRow } from "./SettingsRow";

type Model = components["schemas"]["AgentModelInfo"];

export type ModelTuningControlsProps = {
	models?: Model[];
	model: string;
	effort: string;
	onEffortChange: (value: string) => void;
	onEffortReset?: (value: string) => void;
	onValidityChange?: (valid: boolean) => void;
	variant: "settings" | "composer";
	roleLabel?: string;
	disabled?: boolean;
};

export function useModelTuning(props: Omit<ModelTuningControlsProps, "variant" | "disabled">) {
	const {
		models,
		model,
		effort,
		onEffortChange,
		onEffortReset = onEffortChange,
		onValidityChange,
	} = props;
	const previousModel = useRef(model);
	const previousValidity = useRef<boolean | undefined>(undefined);
	const concreteModel = isConcreteModelID(model) ? model : "";
	const selected =
		(concreteModel ? models?.find((item) => item.id === concreteModel) : undefined) ??
		(concreteModel === "" ? models?.find((item) => item.isDefault && isConcreteModelID(item.id)) : undefined);
	// A model the catalog does not list is still validated against its (empty)
	// capabilities. Only a listed model whose provider never reports efforts is
	// treated as unknown, so a saved effort is kept rather than flagged.
	const capabilitiesKnown = models !== undefined && (!selected || selected.efforts !== undefined);
	const invalidEffort = Boolean(effort && capabilitiesKnown && !selected?.efforts?.includes(effort));

	useEffect(() => {
		if (previousModel.current === model) return;
		if (!capabilitiesKnown) return;
		previousModel.current = model;
		if (effort && !selected?.efforts?.includes(effort)) onEffortReset("");
	}, [capabilitiesKnown, effort, model, onEffortReset, selected]);

	useEffect(() => {
		const valid = !invalidEffort;
		if (previousValidity.current === valid) return;
		previousValidity.current = valid;
		onValidityChange?.(valid);
	}, [invalidEffort, onValidityChange]);
	return { selected, invalidEffort };
}

export function ModelTuningControls(props: ModelTuningControlsProps) {
	const { t } = useTranslation();
	const { effort, onEffortChange, variant, roleLabel, disabled } = props;
	const { selected, invalidEffort } = useModelTuning(props);
	const prefix = roleLabel ? `${roleLabel} ` : "";
	const warning = invalidEffort
		? t("settings.models.unsupportedTuning", { role: roleLabel ? `${roleLabel} ` : "" })
		: null;
	const effortOptions = selected?.efforts?.filter((value) => value && value.toLowerCase() !== "default") ?? [];
	const effortControl = <EffortPicker
		label={`${prefix}${t("settings.models.effort")}`}
		value={effort.toLowerCase() === "default" ? "" : effort}
		choices={effortOptions.map((value) => ({ value }))}
		defaultEffort={selected?.defaultEffort}
		availability={!selected || selected.efforts === undefined ? "unknown" : effortOptions.length ? "supported" : "unsupported"}
		disabled={disabled}
		onChange={onEffortChange}
		triggerClassName={variant === "composer" ? "composer-chip composer-toolbar-option" : "justify-end"}
	/>;
	if (variant === "composer") {
		return effortControl;
	}
	return (
		<>
			{effortControl ? <SettingsRow label={`${prefix}${t("settings.models.effort")}`}>{effortControl}</SettingsRow> : null}
			{warning ? <p role="alert" className="px-1 text-xs leading-row text-warning">{warning}</p> : null}
		</>
	);
}
