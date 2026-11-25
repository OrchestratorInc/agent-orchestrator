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

/** `[ logo | CA ]`: the provider's logo and the account's letters on its color. The letters are
 * always there, so the color is never the only cue. It is as tall as the composer chip's logo. */
export function AccountBadge({ provider, accountProvider, name, email, color, className }: AccountBadgeProps) {
	const label = name?.trim() || email || "";
	const letters = accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span role="img" aria-label={label} title={label || undefined} className={cn("inline-flex h-3.5 shrink-0 items-center gap-0.5", className)}>
			<ProviderMark provider={provider} className="size-3.5" />
			<span aria-hidden="true" className="h-2.5 w-px shrink-0 bg-border" />
			<span
				aria-hidden="true"
				className={cn(
					"inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-[3px] px-0.5 text-[9px] font-semibold leading-none",
					color ? `${COLOR_CLASS[color]} text-(--account-badge-foreground)` : "bg-muted text-foreground",
				)}
			>
				{letters}
			</span>
		</span>
	);
}
