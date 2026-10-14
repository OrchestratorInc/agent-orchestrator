import { useTranslation } from "react-i18next";
import { OptionMenuItem, OptionMenuSub, OptionMenuSubContent, OptionMenuSubTrigger } from "../ui/option-menu";
import type { ModelServiceTier, SpeedMode } from "../../types/conversation";


export function supportsSpeed(tiers: ModelServiceTier[] | undefined, speed: SpeedMode): boolean {
	return speed === "normal" || Boolean(tiers?.some(({ id }) => speed === "fast" ? id === "priority" || id === "fast" : id === "ultrafast"));
}

/** Shared by the chat and new-task pickers; tier availability stays provider-owned. */
export function SpeedMenu({ value = "normal", tiers, disabled, unavailableReason, onChange }: {
	value?: SpeedMode;
	tiers?: ModelServiceTier[];
	disabled?: boolean;
	unavailableReason?: string;
	onChange(value: SpeedMode): void;
}) {
	const { t } = useTranslation();
	const choices: { value: SpeedMode; label: string; description?: string }[] = [
		{ value: "normal", label: t("settings.models.speedNormal") },
		{ value: "fast", label: t("settings.models.speedFast"), description: t("settings.models.speedFastUsage") },
		{ value: "ultrafast", label: t("settings.models.speedUltrafast"), description: t("settings.models.speedUltrafastPlan") },
	];
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label={t("settings.models.speed")} value={choices.find((choice) => choice.value === value)?.label ?? t("settings.models.speedNormal")} disabled={disabled} />
			<OptionMenuSubContent className="w-[min(18rem,calc(100vw-2rem))]">
				{choices.map((choice) => {
					const available = choice.value === "normal" || !unavailableReason && supportsSpeed(tiers, choice.value);
					return (
						<OptionMenuItem key={choice.value} radio active={choice.value === value} disabled={!available}
							onSelect={() => onChange(choice.value)} title={!available ? unavailableReason ?? t("settings.models.speedNotAdvertised") : choice.description}>
							<span className="flex min-w-0 flex-col">
								<span>{choice.label}</span>
								{choice.description ? <span className="whitespace-normal text-xs leading-snug text-muted-foreground">{choice.description}</span> : null}
							</span>
							{!available ? <span className="sr-only">{t("settings.models.speedUnavailable")}</span> : null}
						</OptionMenuItem>
					);
				})}
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}
