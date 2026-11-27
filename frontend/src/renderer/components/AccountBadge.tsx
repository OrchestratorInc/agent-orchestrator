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

// A faint wash of the account's color behind a chip, so the whole chip reads as that account.
const TINT_CLASS: Record<AccountColor, string> = {
	red: "bg-[color-mix(in_oklch,var(--account-color-red)_22%,transparent)]",
	orange: "bg-[color-mix(in_oklch,var(--account-color-orange)_22%,transparent)]",
	amber: "bg-[color-mix(in_oklch,var(--account-color-amber)_22%,transparent)]",
	green: "bg-[color-mix(in_oklch,var(--account-color-green)_22%,transparent)]",
	teal: "bg-[color-mix(in_oklch,var(--account-color-teal)_22%,transparent)]",
	blue: "bg-[color-mix(in_oklch,var(--account-color-blue)_22%,transparent)]",
	violet: "bg-[color-mix(in_oklch,var(--account-color-violet)_22%,transparent)]",
	pink: "bg-[color-mix(in_oklch,var(--account-color-pink)_22%,transparent)]",
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
				"inline-flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-[5px] px-1 text-[10px] font-semibold leading-none",
				color ? `${COLOR_CLASS[color]} text-(--account-badge-foreground)` : "bg-foreground/20 text-foreground",
			)}
		>
			{letters}
		</span>
	);
}

/** An account as one chip: its letters on their color, then its name, all over a wash of that
 * color. Used where the account is the thing being picked, so it reads before any other text. */
export function AccountChip({ provider, accountProvider, name, email, color, label, compact, className }: AccountBadgeProps & {
	label: string;
	/** Letters only, for a tight row; the caller names the account in its tooltip and label. */
	compact?: boolean;
}) {
	const letters = accountAbbreviation({ name, email, provider: accountProvider ?? provider });
	return (
		<span className={cn("inline-flex h-[22px] min-w-0 items-center gap-1.5 rounded-md p-0.5", !compact && "pr-2", color ? TINT_CLASS[color] : "bg-foreground/8", className)}>
			<AccountLetters letters={letters} color={color} />
			{compact ? null : <span className="min-w-0 truncate text-foreground">{label}</span>}
		</span>
	);
}

/** The agent's logo with a dot in the account's color on its corner: which account the message runs
 * on, without taking room from the model's name. The account is named on hover and to screen readers;
 * an account with no color gets a neutral dot. */
export function ProviderMarkWithAccount({ provider, name, email, color, className }: Omit<AccountBadgeProps, "accountProvider">) {
	const label = name?.trim() || email || "";
	return (
		<span role="img" aria-label={label} title={label || undefined} className={cn("relative mr-0.5 inline-flex shrink-0", className)}>
			<ProviderMark provider={provider} className="size-3.5" />
			<span
				aria-hidden="true"
				className={cn(
					"absolute -right-1 -bottom-0.5 size-2 rounded-full shadow-[0_0_0_1.5px_var(--composer-bg,var(--color-bg-tertiary))]",
					color ? COLOR_CLASS[color] : "bg-foreground/45",
				)}
			/>
		</span>
	);
}
