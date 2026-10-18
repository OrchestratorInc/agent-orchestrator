import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { isConcreteModelID } from "../../lib/agent-model-choices";
import { EffortPicker } from "./EffortPicker";
import { SettingsRow } from "./SettingsRow";

type Model = components["schemas"]["AgentModelInfo"];

// ponytail: ladder for gateways that don't advertise capabilities; revisit when providers report efforts.
export const FALLBACK_EFFORTS = ["low", "medium", "high"];

export type EffortChoices = {
	options: string[];
	defaultEffort: string;
	/** True when the ladder is a fallback because the provider did not report efforts. */
	unverified: boolean;
};

export function effortChoices(selected: Model | undefined, hasModel: boolean): EffortChoices {
	const options = (selected?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default");
	if (options.length) {
		const defaultEffort = selected?.defaultEffort ?? "";
		return { options, defaultEffort: options.includes(defaultEffort) ? defaultEffort : "", unverified: false };
	}
	// Lend the fallback ladder whenever the provider reports nothing usable
	// (gateways, off-catalog models): the picker still shows low/medium/high as
	// a best guess instead of dead-ending.
	return { options: hasModel ? FALLBACK_EFFORTS : [], defaultEffort: "", unverified: hasModel };
}

export type ModelTuningControlsProps = {
	models?: Model[];
	model: string;
	effort: string;
	/** Supported launch efforts when no model override is selected. */
	effortsWithoutModel?: readonly string[];
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
		effortsWithoutModel,
		onEffortChange,
		onEffortReset = onEffortChange,
		onValidityChange,
	} = props;
	const previousModel = useRef(model);
	const previousValidity = useRef<boolean | undefined>(undefined);
	const concreteModel = isConcreteModelID(model) ? model : "";
	const selected =
		(concreteModel ? models?.find((item) => item.id === concreteModel) : undefined) ??
		(concreteModel === "" ? models?.find((item) => item.isDefault && isConcreteModelID(item.id)) : undefined) ??
		(concreteModel === "" && effortsWithoutModel ? { id: "", label: "", efforts: [...effortsWithoutModel] } : undefined);
	// A model the catalog does not list is still validated against its (empty)
	// capabilities, but leniently: "default" is not a real effort, comparisons
	// are case-insensitive, and a saved value can't be proven invalid when the
	// provider advertises no efforts (gateways, off-catalog models) — keep it
	// and don't warn.
	const capabilitiesKnown = models !== undefined && (!selected || selected.efforts !== undefined);
	const advertised = (selected?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default");
	const invalidEffort = Boolean(
		effort && effort.toLowerCase() !== "default" && capabilitiesKnown && advertised.length > 0 && !advertised.some((value) => value.toLowerCase() === effort.toLowerCase()),
	);

	useEffect(() => {
		if (previousModel.current === model) return;
		if (!capabilitiesKnown) return;
		previousModel.current = model;
		if (effort && effort.toLowerCase() !== "default" && advertised.length && !advertised.includes(effort)) onEffortReset("");
	}, [capabilitiesKnown, effort, model, onEffortReset, advertised]);

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
	const { effort, model, onEffortChange, variant, roleLabel, disabled } = props;
	const { selected, invalidEffort } = useModelTuning(props);
	const concreteModel = isConcreteModelID(model) ? model : "";
	const choices = effortChoices(selected, Boolean(concreteModel));
	const prefix = roleLabel ? `${roleLabel} ` : "";
	const warning = invalidEffort
		? t("settings.models.unsupportedTuning", { role: roleLabel ? `${roleLabel} ` : "" })
		: null;
	const effortOptions = choices.options;
	const explicitEffort = effort.toLowerCase() === "default" ? "" : effort;
	// A cataloged model whose provider never reports efforts reads as "unknown"
	// (clear-only): the fallback ladder must stay out of the menu there, or the
	// unknown state would silently turn into an empty one. Gateways reporting an
	// empty list and off-catalog models keep the ladder.
	const unreported = Boolean(selected && selected.efforts === undefined);
	const menuChoices = unreported ? [] : effortOptions;
	const unverifiedHint = choices.unverified && !unreported ? t("settings.models.effortUnverified") : null;
	const effortControl = <EffortPicker
		label={`${prefix}${t("settings.models.effort")}`}
		value={explicitEffort}
		choices={menuChoices.map((value) => ({ value }))}
		defaultEffort={choices.defaultEffort || undefined}
		availability={unreported || !selected && !concreteModel ? "unknown" : menuChoices.length ? "supported" : "unsupported"}
		disabled={disabled}
		onChange={onEffortChange}
		triggerClassName={variant === "composer" ? "composer-chip composer-toolbar-option" : "justify-end"}
	/>;
	if (variant === "composer") {
		return effortControl;
	}
	const hasEffortControl = menuChoices.length > 0 || explicitEffort !== "";
	return (
		<>
			{hasEffortControl ? <SettingsRow label={`${prefix}${t("settings.models.effort")}`}>{effortControl}</SettingsRow> : null}
			{unverifiedHint ? <p className="px-1 text-xs leading-row text-settings-muted">{unverifiedHint}</p> : null}
			{warning ? <p role="alert" className="px-1 text-xs leading-row text-warning">{warning}</p> : null}
		</>
	);
}
