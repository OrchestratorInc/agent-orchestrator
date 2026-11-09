import * as React from "react";
import { X } from "@/components/app-icons";
import { Button, type ButtonProps } from "./button";
import { cn } from "../../lib/utils";

/**
 * The ✕ that closes a dialog, sheet or panel. Replaces the old
 * `settings-close-button` utility. Pass `floating` to pin it to the top-right
 * corner of a dialog (the old `settings-dialog-close-button`).
 */
export const CloseButton = React.forwardRef<
	HTMLButtonElement,
	Omit<ButtonProps, "variant" | "children"> & { "aria-label": string; floating?: boolean }
>(({ className, floating = false, size = "icon", type = "button", ...props }, ref) => (
	<Button
		className={cn(floating && "absolute top-3 right-3 z-(--z-chrome)", className)}
		ref={ref}
		size={size}
		type={type}
		variant="quiet"
		{...props}
	>
		<X aria-hidden="true" className={size === "icon-xs" ? "size-3" : size === "icon-sm" ? "size-3.5" : "size-4"} />
	</Button>
));

CloseButton.displayName = "CloseButton";
