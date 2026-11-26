import { RadioGroup as RadioGroupPrimitive } from "radix-ui";
import { useTranslation } from "react-i18next";
import { ACCOUNT_COLORS, type AccountColor } from "../../lib/account-identity";
import type { MessageKey } from "../../i18n/messages";
import { cn } from "../../lib/utils";
import { accountColorClass } from "../AccountBadge";

const COLOR_LABEL_KEYS: Record<AccountColor, MessageKey> = {
	red: "settings.agentAccounts.color.red",
	orange: "settings.agentAccounts.color.orange",
	amber: "settings.agentAccounts.color.amber",
	green: "settings.agentAccounts.color.green",
	teal: "settings.agentAccounts.color.teal",
	blue: "settings.agentAccounts.color.blue",
	violet: "settings.agentAccounts.color.violet",
	pink: "settings.agentAccounts.color.pink",
};

const ITEM_CLASS = "relative inline-flex size-6 shrink-0 items-center justify-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:opacity-50";

/** Eight swatches and "None" as one radio group: arrow keys move, Space picks. The color's name
 * is each swatch's accessible name, and the picked one carries a ring, so color is not the only cue. */
export function AccountColorPicker({ value, onChange, disabled, labelledBy }: {
	value: AccountColor | undefined;
	onChange: (color: AccountColor | undefined) => void;
	disabled?: boolean;
	labelledBy?: string;
}) {
	const { t } = useTranslation();
	return (
		<RadioGroupPrimitive.Root
			value={value ?? ""}
			onValueChange={(next) => onChange(next ? next as AccountColor : undefined)}
			disabled={disabled}
			aria-labelledby={labelledBy}
			aria-label={labelledBy ? undefined : t("settings.agentAccounts.colorLabel")}
			orientation="horizontal"
			className="flex flex-wrap items-center gap-2"
		>
			<RadioGroupPrimitive.Item value="" aria-label={t("settings.agentAccounts.colorNone")} title={t("settings.agentAccounts.colorNone")} className={cn(ITEM_CLASS, "bg-muted text-muted-foreground data-[state=checked]:ring-2 data-[state=checked]:ring-foreground data-[state=checked]:ring-offset-2 data-[state=checked]:ring-offset-background")}>
				<span aria-hidden="true" className="h-px w-3 rotate-45 bg-current" />
			</RadioGroupPrimitive.Item>
			{ACCOUNT_COLORS.map((color) => (
				<RadioGroupPrimitive.Item key={color} value={color} aria-label={t(COLOR_LABEL_KEYS[color])} title={t(COLOR_LABEL_KEYS[color])} className={cn(ITEM_CLASS, accountColorClass(color), "data-[state=checked]:ring-2 data-[state=checked]:ring-foreground data-[state=checked]:ring-offset-2 data-[state=checked]:ring-offset-background")}>
					<RadioGroupPrimitive.Indicator className="size-1.5 rounded-full bg-(--account-badge-foreground)" />
				</RadioGroupPrimitive.Item>
			))}
		</RadioGroupPrimitive.Root>
	);
}
