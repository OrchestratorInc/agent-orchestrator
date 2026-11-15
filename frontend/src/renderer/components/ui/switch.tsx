"use client";

import * as React from "react";
import { Switch as SwitchPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * One switch for the whole app. Size is deliberately not a prop: the control
 * reads as the same affordance in a settings row and in a dense menu row, so a
 * per-call-site size only lets those surfaces drift apart.
 *
 * The thumb sits 1px inside the track (the transparent 1px border) on every
 * side. The track and thumb use inset shading only; pressing changes the
 * track background without moving the control.
 */
function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
	return (
		<SwitchPrimitive.Root
			data-slot="switch"
			className={cn(
				"switch-inset peer relative inline-flex h-4 w-8 shrink-0 cursor-pointer items-center rounded-full border border-transparent transition-[background-color,border-color,box-shadow] duration-fast ease-out outline-none after:absolute after:-inset-x-3 after:-inset-y-2 motion-reduce:transition-none disabled:cursor-not-allowed disabled:opacity-50",
				className,
			)}
			{...props}
		>
			<SwitchPrimitive.Thumb
				data-slot="switch-thumb"
				className="pointer-events-none block size-3.5 rounded-full transition-transform duration-fast ease-out motion-reduce:transition-none data-[state=unchecked]:translate-x-0 data-[state=checked]:translate-x-4"
			/>
		</SwitchPrimitive.Root>
	);
}

export { Switch };
