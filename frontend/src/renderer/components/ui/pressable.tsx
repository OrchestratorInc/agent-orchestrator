import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cn } from "../../lib/utils";

/**
 * Unstyled clickable surface for things that are not action buttons: list and
 * nav rows, cards, tabs, disclosure toggles, table headers. The caller owns
 * layout and color; this only fixes the shared behaviour (button type, no
 * press motion, left-aligned text).
 *
 * Use `Button` for actions. Use `NavRowHighlight` inside a Pressable row when
 * it needs the sidebar's growing hover pill.
 */
export interface PressableProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
	asChild?: boolean;
}

export const Pressable = React.forwardRef<HTMLButtonElement, PressableProps>(
	({ asChild = false, className, type, ...props }, ref) => {
		const Comp = asChild ? Slot : "button";
		return (
			<Comp
				className={cn("text-left outline-none", className)}
				ref={ref}
				type={asChild ? type : (type ?? "button")}
				{...props}
			/>
		);
	},
);

Pressable.displayName = "Pressable";
