import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { orderEffortLevels } from "../../lib/effort";
import { cn } from "../../lib/utils";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "../ui/option-menu";
import { formatEffortLabel } from "./EffortPicker";

export type EffortChipLevel = { value: string; label?: string };

/**
 * The effort column of an agent row: the level's name and a dot meter, beside
 * the agent and model pickers. Levels run lowest to highest.
 */
export function EffortChip({
	value,
	levels,
	onChange,
	"aria-label": ariaLabel,
	disabled,
	className,
}: {
	value: string;
	levels: EffortChipLevel[];
	onChange: (value: string) => void;
	"aria-label": string;
	disabled?: boolean;
	className?: string;
}) {
	const { t } = useTranslation();
	const byValue = new Map(levels.map((level) => [level.value, level]));
	const ordered = orderEffortLevels(levels.map((level) => level.value)).map((level) => byValue.get(level)!);
	if (ordered.length === 0) return null;
	const labelOf = (level: EffortChipLevel) => level.label || formatEffortLabel(level.value, t);
	const activeIndex = ordered.findIndex((level) => level.value === value);
	const current = activeIndex >= 0 ? labelOf(ordered[activeIndex]) : value ? formatEffortLabel(value, t) : t("settings.models.effort");
	return (
		<OptionMenu>
			<OptionMenuTrigger aria-label={ariaLabel} disabled={disabled} className={cn("w-fit gap-1.5", className)}>
				<span className="min-w-0 truncate">{current}</span>
				<EffortMeter filled={activeIndex + 1} total={ordered.length} />
			</OptionMenuTrigger>
			<OptionMenuContent align="end" className="w-[min(12rem,calc(100vw-2rem))]! min-w-0!">
				{ordered.map((level, index) => {
					const active = index === activeIndex;
					return (
						<OptionMenuItem key={level.value} radio active={active} onSelect={() => onChange(level.value)} className="gap-3">
							<span className="flex-1">{labelOf(level)}</span>
							<EffortMeter filled={index + 1} total={ordered.length} />
							<Check aria-hidden="true" className={cn("size-3 shrink-0", !active && "invisible")} />
						</OptionMenuItem>
					);
				})}
			</OptionMenuContent>
		</OptionMenu>
	);
}

/** Dots in the text color: filled up to the level, faint after it. */
export function EffortMeter({ filled, total }: { filled: number; total: number }) {
	return (
		<span aria-hidden="true" data-testid="effort-meter" data-filled={filled} className="inline-flex shrink-0 items-center gap-[2px]">
			{Array.from({ length: total }, (_, index) => (
				<span key={index} className={cn("size-[4px] rounded-full bg-current", index < filled ? "opacity-90" : "opacity-25")} />
			))}
		</span>
	);
}
