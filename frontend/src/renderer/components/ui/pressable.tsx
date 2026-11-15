import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cn } from "../../lib/utils";

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
