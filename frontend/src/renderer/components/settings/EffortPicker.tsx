import type { TFunction } from "i18next";
import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "../ui/option-menu";

export type EffortChoice = { value: string; label?: string };
export type EffortAvailability = "supported" | "unsupported" | "unknown" | "launch-unavailable";
export type EffortMenuProps = {
	value: string;
	choices: EffortChoice[];
	onChange: (value: string) => void;
	defaultValue?: string | null;
	defaultEffort?: string;
	availability?: EffortAvailability;
	/** Chat composer menus: 12px rows with muted inactive items, like the model list. */
	compact?: boolean;
};

const EFFORT_LEVEL_KEYS = {
	none: "settings.models.effortLevel.none",
	minimal: "settings.models.effortLevel.minimal",
	low: "settings.models.effortLevel.low",
	medium: "settings.models.effortLevel.medium",
	high: "settings.models.effortLevel.high",
	xhigh: "settings.models.effortLevel.xhigh",
	max: "settings.models.effortLevel.max",
} as const;

function effortLevelKey(value: string): (typeof EFFORT_LEVEL_KEYS)[keyof typeof EFFORT_LEVEL_KEYS] | undefined {
	return Object.hasOwn(EFFORT_LEVEL_KEYS, value) ? EFFORT_LEVEL_KEYS[value as keyof typeof EFFORT_LEVEL_KEYS] : undefined;
}

// Known levels are localized; anything else a provider reports is shown as-is,
// capitalized, because we cannot translate names we have never seen.
export function formatEffortLabel(value: string, t: TFunction): string {
	const key = effortLevelKey(value.trim().toLowerCase());
	return key ? t(key) : value.charAt(0).toUpperCase() + value.slice(1);
}

type EffortDisplayLabelInput = {
	value: string;
	choices: EffortChoice[];
	followLabel: string;
	t: TFunction;
	defaultEffort?: string;
};

export function effortDisplayLabel({ value, choices, followLabel, t, defaultEffort }: EffortDisplayLabelInput): string {
	if (value) return choices.find((choice) => choice.value === value)?.label || formatEffortLabel(value, t);
	const reported = defaultEffort?.trim().toLowerCase();
	if (!reported) return followLabel;
	const choice = choices.find(
		(item) => item.value.toLowerCase() !== "default" && (item.value.toLowerCase() === reported || item.label?.toLowerCase() === reported),
	);
	if (choice) return choice.label || formatEffortLabel(choice.value, t);
	// A provider's default description may be prose, so only a known level name
	// is trusted as a label.
	return effortLevelKey(reported) ? formatEffortLabel(reported, t) : followLabel;
}

export function EffortMenuItems({ value, choices, onChange, defaultValue = "", defaultEffort, availability = "supported", compact = false }: EffortMenuProps) {
	const { t } = useTranslation();
	const rowSize = compact ? "text-xs" : "text-[length:var(--font-size-base)]";
	const levels = choices.filter((choice) => choice.value !== defaultValue && choice.value.toLowerCase() !== "default");
	const following = value === defaultValue || (!value && defaultValue === "default");
	const unknown = value && !following && !levels.some((choice) => choice.value === value);
	const unavailable = availability !== "supported";
	const reportedDefault = defaultEffort?.trim().toLowerCase();
	const defaultChoice = !unavailable && reportedDefault
		? levels.find((choice) => choice.value.toLowerCase() === reportedDefault || choice.label?.toLowerCase() === reportedDefault)
		: undefined;
	return <>
		{unknown ? <OptionMenuItem disabled className={cn(rowSize, "text-muted-foreground")}>
			{t(availability === "unknown" ? "settings.models.currentEffort" : "settings.models.savedEffortUnavailable", { effort: formatEffortLabel(value, t) })}
		</OptionMenuItem> : null}
		{!unavailable && levels.map((choice) => {
			const isDefault = choice === defaultChoice;
			const active = choice.value === value || (following && isDefault);
			return <OptionMenuItem key={choice.value} radio active={active}
				onSelect={() => onChange(isDefault && defaultValue !== null ? defaultValue : choice.value)} className={cn(rowSize, compact && !active ? "text-muted-foreground" : "text-foreground")}>
				<span className="flex-1">{choice.label || formatEffortLabel(choice.value, t)}</span>
				<Check aria-hidden="true" className={`ml-3 size-3 shrink-0 ${active ? "" : "invisible"}`} />
			</OptionMenuItem>;
		})}
		{unknown && defaultValue !== null ? <OptionMenuItem onSelect={() => onChange(defaultValue)}>{t("settings.models.clearEffort")}</OptionMenuItem> : null}
	</>;
}

export function EffortPicker(props: EffortMenuProps & { disabled?: boolean; label?: string; triggerClassName?: string; menuClassName?: string }) {
	const { t } = useTranslation();
	const following = props.value === (props.defaultValue ?? "") || (!props.value && props.defaultValue === "default");
	const label = effortDisplayLabel({
		value: following ? "" : props.value,
		choices: props.choices,
		followLabel: t("settings.models.effort"),
		t,
		defaultEffort: props.defaultEffort,
	});
	if (!props.choices.some((choice) => choice.value && choice.value.toLowerCase() !== "default") && (!props.value || following)) return null;
	return <OptionMenu>
		<OptionMenuTrigger disabled={props.disabled} aria-label={props.label || t("settings.models.effort")} className={props.triggerClassName}>
			<span className="min-w-0 truncate">{label}</span>
		</OptionMenuTrigger>
		<OptionMenuContent align="end" className={cn("w-[min(16rem,calc(100vw-2rem))]! min-w-0! max-w-[calc(100vw-2rem)]!", props.menuClassName)}><EffortMenuItems {...props} /></OptionMenuContent>
	</OptionMenu>;
}
