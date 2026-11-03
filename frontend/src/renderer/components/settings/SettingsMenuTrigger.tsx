/**
 * The dropdown chrome Settings is drawn with: a quiet filled trigger that only
 * gains contrast on hover and while open, and a rounded panel whose entries read
 * as settings rows rather than menu lines.
 *
 * It lives apart from SettingsOptionMenu because that component is a list of
 * values — one label per entry — and surfaces outside Settings need the same
 * dropdown around entries that are not that shape. The chat composer's turn
 * settings put a description under each model and a substitution notice above
 * the list, so they take the chrome and keep their own contents.
 */

import { ChevronDown } from "lucide-react";
import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";
import { MENU_TRIGGER_CHROME } from "../ui/option-menu";

/**
 * The panel. bg-settings-menu / border-settings-menu / the panel radius have to
 * be real utilities so twMerge drops DropdownMenuContent's bg-popover,
 * border-border and rounded-lg rather than leaving both in the class list.
 */
export const SETTINGS_MENU_SURFACE =
	"settings-menu-surface min-w-[length:var(--size-settings-menu-min-width)] rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu shadow-(--shadow-popover)";

/** An entry whose whole content is one label, as on the settings rows. */
export const SETTINGS_MENU_ITEM =
	"settings-menu-item min-w-0 cursor-default outline-none focus:bg-interactive-hover focus:text-foreground data-highlighted:bg-interactive-hover data-highlighted:text-foreground";

/**
 * The same box for entries that carry their own layout — a name over a hint, a
 * trailing marker. Spelled out in plain utilities rather than reusing the
 * settings-menu-item utility, whose flex row would have to be undone by the
 * caller and can only be overridden by utility ordering.
 */
export const SETTINGS_MENU_ROW =
	"min-w-0 cursor-default rounded-md px-2 py-1.5 text-control text-muted-foreground outline-none focus:bg-interactive-hover focus:text-foreground data-highlighted:bg-interactive-hover data-highlighted:text-foreground";

/** A heading inside the panel, inset to the entries rather than the panel edge. */
export const SETTINGS_MENU_LABEL =
	"px-2 py-1.5 text-micro tracking-wide text-passive";

const TRIGGER = cn("group/settings-option-trigger", MENU_TRIGGER_CHROME);

/**
 * The trigger. Pass it to DropdownMenuTrigger with `asChild`; the chevron is
 * appended after whatever the caller renders and flips when the menu opens.
 */
export const SettingsMenuTrigger = forwardRef<
	HTMLButtonElement,
	ButtonHTMLAttributes<HTMLButtonElement> & { showCaret?: boolean }
>(function SettingsMenuTrigger({ className, children, showCaret = true, ...props }, ref) {
	return (
		<button ref={ref} type="button" className={cn(TRIGGER, className)} {...props}>
			{children}
			{showCaret ? (
				<ChevronDown
					className="size-icon-sm shrink-0 transition-transform duration-300 ease-out group-data-[state=open]/settings-option-trigger:rotate-180"
					aria-hidden="true"
				/>
			) : null}
		</button>
	);
});
