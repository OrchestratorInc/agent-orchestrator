import { Tooltip as TooltipPrimitive } from "radix-ui";
import * as React from "react";
import { cn } from "../../lib/utils";

function TooltipProvider({
	delayDuration = 400,
	disableHoverableContent = true,
	...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
	return (
		<TooltipPrimitive.Provider
			data-slot="tooltip-provider"
			delayDuration={delayDuration}
			disableHoverableContent={disableHoverableContent}
			{...props}
		/>
	);
}

function Tooltip({ ...props }: React.ComponentProps<typeof TooltipPrimitive.Root>) {
	return <TooltipPrimitive.Root data-slot="tooltip" {...props} />;
}

function TooltipTrigger({ ...props }: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
	return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />;
}

function TooltipContent({
	className,
	sideOffset = 0,
	children,
	...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
	return (
		<TooltipPrimitive.Portal>
			<TooltipPrimitive.Content
				data-slot="tooltip-content"
				sideOffset={sideOffset}
				className={cn(
					"pointer-events-none z-50 w-fit origin-(--radix-tooltip-content-transform-origin) animate-in rounded-md bg-popover px-3 py-1.5 text-xs text-balance text-popover-foreground shadow-md fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
					className,
				)}
				{...props}
			>
				{children}
				<TooltipPrimitive.Arrow className="z-50 size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-[2px] bg-popover fill-popover" />
			</TooltipPrimitive.Content>
		</TooltipPrimitive.Portal>
	);
}

// Same delay/skip-delay the shared TooltipProvider applies.
const LAZY_TOOLTIP_DELAY_MS = 400;
const LAZY_TOOLTIP_SKIP_DELAY_MS = 300;
let lazyTooltipLastClosedAt = 0;

/**
 * Tooltip that mounts no Radix Tooltip/Popper until the trigger is first hovered
 * or keyboard-focused. For repeated row actions, where hundreds of idle Tooltip
 * roots would otherwise re-render with every parent update. The trigger is
 * wrapped in an inline-flex span (the anchor); same delay, content and
 * dismissal as {@link Tooltip}. The child keeps its own aria-label.
 */
function LazyTooltip({
	children,
	content,
	side,
	className,
}: {
	children: React.ReactNode;
	content: React.ReactNode;
	side?: React.ComponentProps<typeof TooltipPrimitive.Content>["side"];
	className?: string;
}) {
	const [armed, setArmed] = React.useState(false);
	const [open, setOpen] = React.useState(false);
	const timerRef = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	React.useEffect(() => () => clearTimeout(timerRef.current), []);

	const close = () => {
		clearTimeout(timerRef.current);
		setOpen((wasOpen) => {
			if (wasOpen) lazyTooltipLastClosedAt = Date.now();
			return false;
		});
	};
	const enter = () => {
		setArmed(true);
		clearTimeout(timerRef.current);
		if (Date.now() - lazyTooltipLastClosedAt < LAZY_TOOLTIP_SKIP_DELAY_MS) setOpen(true);
		else timerRef.current = setTimeout(() => setOpen(true), LAZY_TOOLTIP_DELAY_MS);
	};

	return (
		<span
			className={cn("relative inline-flex", className)}
			onBlur={close}
			onFocus={(event) => {
				if (!event.target.matches?.(":focus-visible")) return;
				setArmed(true);
				clearTimeout(timerRef.current);
				setOpen(true);
			}}
			onPointerDown={close}
			onPointerEnter={(event) => {
				if (event.pointerType !== "touch") enter();
			}}
			onPointerLeave={close}
		>
			{children}
			{armed ? (
				<TooltipPrimitive.Root open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
					<TooltipPrimitive.Trigger asChild>
						<span aria-hidden="true" className="pointer-events-none absolute inset-0" />
					</TooltipPrimitive.Trigger>
					<TooltipContent side={side}>{content}</TooltipContent>
				</TooltipPrimitive.Root>
			) : null}
		</span>
	);
}

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider, LazyTooltip };
