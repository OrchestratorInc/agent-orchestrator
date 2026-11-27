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
	/** The letters from accountLetters, unique among the provider's accounts. Without them the badge
	 * falls back to this account's own abbreviation, which another account may share. */
	letters?: string;
	className?: string;
}

/** The provider's logo, then a separate square with the account's letters on its color. The letters
 * are always there, so the color is never the only cue. No outer frame: inside a chip or a row, a
 * border around the pair reads as a nested control. */
export function AccountBadge({ provider, accountProvider, name, email, color, letters: given, className }: AccountBadgeProps) {
	const label = name?.trim() || email || "";
	const letters = given || accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span
			role="img"
			aria-label={label}
			title={label || undefined}
			className={cn("inline-flex h-[18px] shrink-0 items-center gap-1", className)}
		>
			<ProviderMark provider={provider} className="size-3.5" />
			<AccountLetters letters={letters} color={color} />
		</span>
	);
}

/** The account's letters on its color: a solid square, so the color reads at a glance. */
function AccountLetters({ letters, color }: { letters: string; color?: AccountColor }) {
	return (
		<span
			aria-hidden="true"
			className={cn(
				"inline-flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-[5px] px-1 text-[length:10px] font-semibold leading-none",
				color ? `${COLOR_CLASS[color]} text-(color:--account-badge-foreground)` : "bg-foreground/20 text-foreground",
			)}
		>
			{letters}
		</span>
	);
}

/** An account as its letters on their color, then its name in plain text. Used where the account is
 * the thing being picked. */
export function AccountChip({ provider, accountProvider, name, email, color, letters: given, label, compact, className }: AccountBadgeProps & {
	label: string;
	/** Letters only, for a tight row; the caller names the account in its tooltip and label. */
	compact?: boolean;
}) {
	const letters = given || accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span className={cn("inline-flex min-w-0 items-center gap-2", className)}>
			<AccountLetters letters={letters} color={color} />
			{compact ? null : <span className="min-w-0 truncate text-foreground">{label}</span>}
		</span>
	);
}

/** The agent's logo with a small circle in the account's color on its corner, holding the first of
 * the account's letters: which account the message runs on, without taking room from the model's name. The text is
 * the dark badge foreground, which holds >= 4.5:1 on every palette color in both themes (white does
 * not). The account is named on hover and to screen readers. */
export function ProviderMarkWithAccount({ provider, accountProvider, name, email, color, letters: given, className }: AccountBadgeProps) {
	const label = name?.trim() || email || "";
	const initial = Array.from(given || accountAbbreviation({ name, email, provider: accountProvider ?? provider }))[0] ?? "";
	return (
		<span role="img" aria-label={label} title={label || undefined} className={cn("relative mr-1.5 inline-flex shrink-0", className)}>
			<ProviderMark provider={provider} className="size-3.5" />
			<span
				aria-hidden="true"
				className={cn(
					"absolute -right-1.5 -bottom-px inline-flex size-[10px] items-center justify-center rounded-full text-[length:6.5px] font-bold leading-none",
					color ? `${COLOR_CLASS[color]} text-(color:--account-badge-foreground)` : "bg-foreground/45 text-background",
				)}
			>
				{initial}
			</span>
		</span>
	);
}
