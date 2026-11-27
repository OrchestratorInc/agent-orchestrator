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

/** Eight swatches as one radio group: arrow keys move, Space picks. The color's name is each
 * swatch's accessible name, and the picked one carries a ring, so color is not the only cue. A color
 * another account of the provider uses is disabled and says which account, since no two accounts
 * share one. `value` is empty only for a new account, which the daemon then gives a free color. */
export function AccountColorPicker({ value, onChange, taken, disabled, labelledBy }: {
	value: AccountColor | undefined;
	onChange: (color: AccountColor) => void;
	/** The colors other accounts of the provider use, each with the account's letters and label. */
	taken?: ReadonlyMap<AccountColor, string>;
	disabled?: boolean;
	labelledBy?: string;
}) {
	const { t } = useTranslation();
	return (
		<RadioGroupPrimitive.Root
			value={value ?? ""}
			onValueChange={(next) => { if (next) onChange(next as AccountColor); }}
			disabled={disabled}
			aria-labelledby={labelledBy}
			aria-label={labelledBy ? undefined : t("settings.agentAccounts.colorLabel")}
			orientation="horizontal"
			className="flex flex-wrap items-center gap-2"
		>
			{ACCOUNT_COLORS.map((color) => {
				const owner = color === value ? undefined : taken?.get(color);
				const label = owner ? t("settings.agentAccounts.colorTaken", { color: t(COLOR_LABEL_KEYS[color]), account: owner }) : t(COLOR_LABEL_KEYS[color]);
				// A disabled button shows no tooltip, so the wrapper carries it.
				return (
					<span key={color} title={label} className="inline-flex">
						<RadioGroupPrimitive.Item value={color} disabled={Boolean(owner)} aria-label={label} className={cn(ITEM_CLASS, accountColorClass(color), "data-[state=checked]:ring-2 data-[state=checked]:ring-foreground data-[state=checked]:ring-offset-2 data-[state=checked]:ring-offset-background", owner && "opacity-25")}>
							<RadioGroupPrimitive.Indicator className="size-1.5 rounded-full bg-(--account-badge-foreground)" />
						</RadioGroupPrimitive.Item>
					</span>
				);
			})}
		</RadioGroupPrimitive.Root>
	);
}

/** The colors the provider's other accounts use, for AccountColorPicker's `taken`: each with the
 * account's letters and label, such as "RO · ronish@untrivial.ai". */
export function takenAccountColors<T extends { id: string; provider: string; color?: AccountColor }>(
	accounts: readonly T[],
	self: { id?: string; provider: string },
	describe: (account: T) => string,
): Map<AccountColor, string> {
	const taken = new Map<AccountColor, string>();
	for (const account of accounts) {
		if (account.provider !== self.provider || account.id === self.id || !account.color || taken.has(account.color)) continue;
		taken.set(account.color, describe(account));
	}
	return taken;
}
