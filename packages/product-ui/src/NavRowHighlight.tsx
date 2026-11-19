import { cn } from "./utils";

/**
 * Host class for rows that use {@link NavRowHighlight}.
 *
 * MUST keep host backgrounds transparent on hover/active/focus — the
 * highlight owns the fill. Painting `hover:bg-interactive-hover` on the host again
 * doubles the highlight (sidebar + home recent-row regression).
 */
export const NAV_ROW_HIGHLIGHT_HOST_CLASS =
	"group/nav-row relative hover:bg-transparent! focus-visible:bg-transparent! active:bg-transparent! data-[active=true]:bg-transparent! hover:text-foreground data-[active=true]:font-medium data-[active=true]:text-foreground";

/**
 * Fill behind nav / home recent rows.
 *
 * Design contracts — do not regress:
 * - Always the host's full size. Hover/focus shows it at once: no grow, no
 *   fade, no transition, so the row answers the pointer instantly.
 * - Fine-pointer hover is gated in `styles.css` (`[data-nav-row-highlight-idle]`).
 *   Keyboard uses `:focus-visible` / `:has(:focus-visible)` there — NOT
 *   `:focus-within` (mouse click focus would stick the pill on after toggle).
 * - Shared by Sidebar and HomePage recent rows; keep one implementation.
 *   `className` may only swap the fill (home uses the action-card surface).
 */
export function NavRowHighlight({
	active = false,
	className,
	disabled = false,
}: {
	active?: boolean;
	className?: string;
	disabled?: boolean;
}) {
	return (
		<span
			aria-hidden="true"
			className={cn(
				"pointer-events-none absolute inset-0 rounded-lg bg-interactive-hover",
				active ? "bg-interactive-active opacity-100" : "opacity-0",
				disabled && !active && "opacity-0!",
				className,
			)}
			data-nav-row-highlight=""
			data-nav-row-highlight-idle={active || disabled ? undefined : ""}
		/>
	);
}
