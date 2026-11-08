import { Mail } from "lucide-react";
import { cn } from "../../lib/utils";
import { avatarInitials, teamInitials } from "../../hooks/useTeams";

/**
 * A person: a circle with their initials, or a dashed circle with a mail mark
 * while they are only an email. Teams are rounded squares (TeamTile), so the
 * two read differently without color.
 */
export function PersonAvatar({ name, email, invited, size = "md", className }: {
	name?: string;
	email?: string;
	invited?: boolean;
	size?: "xs" | "sm" | "md";
	className?: string;
}) {
	const box = size === "xs" ? "size-4 text-[7.5px]" : size === "sm" ? "size-5 text-[9px]" : "size-6 text-micro";
	if (invited || (!name && email)) {
		return (
			<span
				aria-hidden="true"
				className={cn("inline-grid shrink-0 place-items-center rounded-full border border-dashed border-foreground/25 text-muted-foreground", box, className)}
				data-person-avatar="email"
			>
				<Mail className={size === "md" ? "size-3" : "size-2.5"} />
			</span>
		);
	}
	return (
		<span
			aria-hidden="true"
			className={cn("inline-grid shrink-0 place-items-center rounded-full bg-muted font-semibold tracking-[0.01em] text-muted-foreground", box, className)}
			data-person-avatar=""
		>
			{avatarInitials(name, email)}
		</span>
	);
}

export function TeamTile({ name, size = "md", className }: { name: string; size?: "sm" | "md" | "lg" | "xl"; className?: string }) {
	const box =
		size === "sm" ? "size-5 rounded-[5px] text-[8.5px]"
		: size === "lg" ? "size-7 rounded-[7px] text-2xs"
		: size === "xl" ? "size-10 rounded-[9px] text-[13px]"
		: "size-6 rounded-sm text-[9.5px]";
	return (
		<span
			aria-hidden="true"
			className={cn("inline-grid shrink-0 place-items-center bg-foreground/[0.11] font-semibold tracking-[0.02em] text-foreground/80", box, className)}
			data-team-tile=""
		>
			{teamInitials(name)}
		</span>
	);
}
