import { ChevronDown } from "@/components/app-icons";
import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";
import { MENU_GHOST_TRIGGER_CHROME, MENU_TRIGGER_CHROME } from "../ui/option-menu";
import { Pressable } from "../ui/pressable";

export const SETTINGS_MENU_SURFACE =
	"settings-menu-surface min-w-[length:var(--size-settings-menu-min-width)] rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu shadow-(--shadow-popover)";

export const SETTINGS_MENU_ITEM =
	"settings-menu-item min-w-0 cursor-default outline-none focus:bg-interactive-hover focus:text-foreground data-highlighted:bg-interactive-hover data-highlighted:text-foreground";

export const SETTINGS_MENU_ROW =
	"min-w-0 cursor-default rounded-md px-2 py-1.5 text-control text-muted-foreground outline-none focus:bg-interactive-hover focus:text-foreground data-highlighted:bg-interactive-hover data-highlighted:text-foreground";

export const SETTINGS_MENU_LABEL =
	"px-2 py-1.5 text-micro tracking-wide text-passive";

export const SettingsMenuTrigger = forwardRef<
	HTMLButtonElement,
	ButtonHTMLAttributes<HTMLButtonElement> & { showCaret?: boolean; variant?: "secondary" | "ghost" }
>(function SettingsMenuTrigger({ className, children, showCaret = true, variant = "secondary", ...props }, ref) {
	return (
		<Pressable ref={ref} className={cn("group/settings-option-trigger", variant === "ghost" ? MENU_GHOST_TRIGGER_CHROME : MENU_TRIGGER_CHROME, className)} {...props}>
			{children}
			{showCaret ? (
				<ChevronDown
					className="size-icon-sm shrink-0 transition-transform duration-300 ease-out group-data-[state=open]/settings-option-trigger:rotate-180"
					aria-hidden="true"
				/>
			) : null}
		</Pressable>
	);
});
