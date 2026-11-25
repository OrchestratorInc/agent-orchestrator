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
 * always there, so the color is never the only cue. One bordered segment split by a divider, 16px tall so the 14px logo fits inside. */
export function AccountBadge({ provider, accountProvider, name, email, color, className }: AccountBadgeProps) {
	const label = name?.trim() || email || "";
	const letters = accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span
			role="img"
			aria-label={label}
			title={label || undefined}
			className={cn("inline-flex h-4 shrink-0 items-stretch overflow-hidden rounded-[4px] border border-border", className)}
		>
			<span aria-hidden="true" className="inline-flex items-center px-0.5">
				<ProviderMark provider={provider} className="size-3.5" />
			</span>
			<span
				aria-hidden="true"
				className={cn(
					"inline-flex min-w-3.5 items-center justify-center border-l border-border px-1 text-[9px] font-semibold leading-none",
					color ? `${COLOR_CLASS[color]} text-(--account-badge-foreground)` : "bg-muted text-foreground",
				)}
			>
				{letters}
			</span>
		</span>
	);
}
