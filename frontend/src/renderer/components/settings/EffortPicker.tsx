import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "../ui/option-menu";

export type EffortChoice = { value: string; label?: string };
export type EffortMenuProps = {
	value: string;
	choices: EffortChoice[];
	onChange: (value: string) => void;
	defaultValue?: string | null;
	defaultEffort?: string;
	availability?: "supported" | "unsupported" | "unknown" | "launch-unavailable";
};

export function formatEffortLabel(value: string): string {
	return value === "xhigh" ? "Extra high" : value.charAt(0).toUpperCase() + value.slice(1);
}

export function effortDisplayLabel(value: string, choices: EffortChoice[], followLabel: string, defaultEffort?: string): string {
	if (value) return choices.find((choice) => choice.value === value)?.label || formatEffortLabel(value);
	const reported = defaultEffort?.trim().toLowerCase();
	const choice = reported ? choices.find((choice) => choice.value.toLowerCase() !== "default" && (choice.value.toLowerCase() === reported || choice.label?.toLowerCase() === reported)) : undefined;
	const defaultLabel = choice?.label || (choice ? formatEffortLabel(choice.value) : reported && ["none", "off", "minimal", "low", "medium", "high", "xhigh", "max"].includes(reported) ? formatEffortLabel(reported) : "");
	return defaultLabel || followLabel;
}

export function EffortMenuItems({ value, choices, onChange, defaultValue = "", defaultEffort, availability = "supported" }: EffortMenuProps) {
	const { t } = useTranslation();
	const levels = choices.filter((choice) => choice.value !== defaultValue && choice.value.toLowerCase() !== "default");
	const following = value === defaultValue || (!value && defaultValue === "default");
	const unknown = value && !following && !levels.some((choice) => choice.value === value);
	const unavailable = availability !== "supported";
	const reportedDefault = defaultEffort?.trim().toLowerCase();
	const defaultChoice = !unavailable && reportedDefault
		? levels.find((choice) => choice.value.toLowerCase() === reportedDefault || choice.label?.toLowerCase() === reportedDefault)
		: undefined;
	return <>
		{unknown ? <OptionMenuItem disabled className="text-[length:var(--font-size-base)] text-muted-foreground">
			{t(availability === "unknown" ? "settings.models.currentEffort" : "settings.models.savedEffortUnavailable", { effort: formatEffortLabel(value) })}
		</OptionMenuItem> : null}
		{!unavailable && levels.map((choice) => {
			const isDefault = choice === defaultChoice;
			const active = choice.value === value || (following && isDefault);
			return <OptionMenuItem key={choice.value} radio active={active}
				onSelect={() => onChange(isDefault && defaultValue !== null ? defaultValue : choice.value)} className="text-[length:var(--font-size-base)] text-foreground">
				<span className="flex-1">{choice.label || formatEffortLabel(choice.value)}</span>
				<Check aria-hidden="true" className={`ml-3 size-3 shrink-0 ${active ? "" : "invisible"}`} />
			</OptionMenuItem>;
		})}
		{unknown && defaultValue !== null ? <OptionMenuItem onSelect={() => onChange(defaultValue)}>{t("settings.models.clearEffort")}</OptionMenuItem> : null}
	</>;
}

export function EffortPicker(props: EffortMenuProps & { disabled?: boolean; label?: string; triggerClassName?: string }) {
	const { t } = useTranslation();
	const following = props.value === (props.defaultValue ?? "") || (!props.value && props.defaultValue === "default");
	const label = !following && props.value
		? effortDisplayLabel(props.value, props.choices, t("settings.models.effort"))
		: effortDisplayLabel("", props.choices, t("settings.models.effort"), props.defaultEffort);
	if (!props.choices.some((choice) => choice.value && choice.value.toLowerCase() !== "default") && (!props.value || following)) return null;
	return <OptionMenu>
		<OptionMenuTrigger disabled={props.disabled} aria-label={props.label || t("settings.models.effort")} className={props.triggerClassName}>
			<span className="min-w-0 truncate">{label}</span>
		</OptionMenuTrigger>
		<OptionMenuContent align="end" className="w-[min(16rem,calc(100vw-2rem))]! min-w-0! max-w-[calc(100vw-2rem)]!"><EffortMenuItems {...props} /></OptionMenuContent>
	</OptionMenu>;
}
