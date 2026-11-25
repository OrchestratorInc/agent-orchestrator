import type { AccountColor } from "../lib/account-identity";
import { accountAbbreviation } from "../lib/account-identity";
import { cn } from "../lib/utils";
import { ProviderMark } from "./chat/ProviderMark";

// Full class names, so Tailwind sees each one.
const COLOR_CLASS: Record<AccountColor, string> = {
	red: "bg-(--account-color-red)",
	orange: "bg-(--account-color-orange)",
	amber: "bg-(--account-color-amber)",
	green: "bg-(--account-color-green)",
	teal: "bg-(--account-color-teal)",
	blue: "bg-(--account-color-blue)",
	violet: "bg-(--account-color-violet)",
	pink: "bg-(--account-color-pink)",
};

/** The class that paints a swatch or segment in an account color token. */
export const accountColorClass = (color: AccountColor) => COLOR_CLASS[color];

export interface AccountBadgeProps {
	/** The agent whose logo leads the badge. */
	provider: string;
	/** The account's provider, when it differs from the logo's agent id (claude-code is "claude"). */
	accountProvider?: string;
	name?: string;
	email?: string;
	color?: AccountColor;
	className?: string;
}

/** The provider's logo, then a separate square with the account's letters on its color. The letters
 * are always there, so the color is never the only cue. No outer frame: inside a chip or a row, a
 * border around the pair reads as a nested control. */
export function AccountBadge({ provider, accountProvider, name, email, color, className }: AccountBadgeProps) {
	const label = name?.trim() || email || "";
	const letters = accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span
			role="img"
			aria-label={label}
			title={label || undefined}
			className={cn("inline-flex h-4 shrink-0 items-center gap-1", className)}
		>
			<ProviderMark provider={provider} className="size-3.5" />
			<span
				aria-hidden="true"
				className={cn(
					"inline-flex h-4 min-w-4 items-center justify-center rounded-[4px] px-1 text-[9px] font-semibold leading-none",
					color ? `${COLOR_CLASS[color]} text-(--account-badge-foreground)` : "bg-foreground/15 text-foreground",
				)}
			>
				{letters}
			</span>
		</span>
	);
}
