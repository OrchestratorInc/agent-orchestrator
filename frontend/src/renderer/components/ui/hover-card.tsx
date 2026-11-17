import { HoverCard as HoverCardPrimitive } from "radix-ui";
import { useCallback, useEffect, useState } from "react";
import { cn } from "../../lib/utils";

/**
 * Closes when anything outside the card scrolls. Radix closes a hover card only
 * on pointer leave, and a scroll moves the trigger out from under a pointer that
 * stays put: in a chat that auto-scrolls while it streams, the card never got a
 * leave event and stayed open over the timeline. Scrolling inside the card keeps
 * it open.
 */
function HoverCard({
	openDelay = 300,
	closeDelay = 150,
	open: openProp,
	defaultOpen = false,
	onOpenChange,
	...props
}: React.ComponentProps<typeof HoverCardPrimitive.Root>) {
	const [uncontrolledOpen, setUncontrolledOpen] = useState(defaultOpen);
	const open = openProp ?? uncontrolledOpen;
	const setOpen = useCallback(
		(next: boolean) => {
			if (openProp === undefined) setUncontrolledOpen(next);
			onOpenChange?.(next);
		},
		[openProp, onOpenChange],
	);
	useEffect(() => {
		if (!open) return;
		const dismiss = (event: Event) => {
			if (event.target instanceof Element && event.target.closest("[data-slot=hover-card-content]")) return;
			setOpen(false);
		};
		window.addEventListener("scroll", dismiss, { capture: true, passive: true });
		return () => window.removeEventListener("scroll", dismiss, { capture: true });
	}, [open, setOpen]);
	return (
		<HoverCardPrimitive.Root
			data-slot="hover-card"
			openDelay={openDelay}
			closeDelay={closeDelay}
			open={open}
			onOpenChange={setOpen}
			{...props}
		/>
	);
}

function HoverCardTrigger({ ...props }: React.ComponentProps<typeof HoverCardPrimitive.Trigger>) {
	return <HoverCardPrimitive.Trigger data-slot="hover-card-trigger" {...props} />;
}

function HoverCardContent({
	className,
	align = "start",
	sideOffset = 6,
	portalContainer,
	animation = "full",
	...props
}: React.ComponentProps<typeof HoverCardPrimitive.Content> & {
	portalContainer?: React.ComponentProps<typeof HoverCardPrimitive.Portal>["container"];
	/**
	 * "full" fades and zooms in and out. "enter" fades and slides in only, so
	 * closing is instant. "none" appears and disappears instantly.
	 */
	animation?: "full" | "enter" | "none";
}) {
	return (
		<HoverCardPrimitive.Portal container={portalContainer}>
			<HoverCardPrimitive.Content
				data-slot="hover-card-content"
				align={align}
				sideOffset={sideOffset}
				className={cn(
					"z-overlay w-72 rounded-lg border border-(--color-border-overlay) bg-overlay text-popover-foreground shadow-(--shadow-popover) outline-none",
					animation === "full" &&
						"origin-(--radix-hover-card-content-transform-origin) animate-in fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
					animation === "enter" &&
						"animate-in fade-in-0 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
					className,
				)}
				{...props}
			/>
		</HoverCardPrimitive.Portal>
	);
}

export { HoverCard, HoverCardTrigger, HoverCardContent };
