import { Tooltip as TooltipPrimitive } from "radix-ui";
import { effectiveShortcutBindings, shortcutBindingKeys, type AppShortcutId } from "../../../shared/shortcuts";
import { isMacPlatform } from "../../lib/platform";
import { cn } from "../../lib/utils";
import { useKeybindingsStore } from "../../stores/keybindings-store";

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

/** The user's current first binding for a command (honours custom keybindings), or undefined if unbound. */
function useShortcutKeys(id: AppShortcutId | undefined): readonly string[] | undefined {
	const overrides = useKeybindingsStore((state) => state.overrides);
	if (!id) return undefined;
	const isMac = isMacPlatform();
	const binding = effectiveShortcutBindings(id, isMac, overrides)[0];
	return binding ? shortcutBindingKeys(binding, isMac) : undefined;
}

function TooltipContent({
	className,
	sideOffset = 0,
	children,
	hideArrow = false,
	shortcut,
	...props
}: React.ComponentProps<typeof TooltipPrimitive.Content> & {
	hideArrow?: boolean;
	/** Command whose keybinding is shown beside the label as muted glyphs (⌘ K), like the Cmd+K palette. */
	shortcut?: AppShortcutId;
}) {
	const keys = useShortcutKeys(shortcut);
	return (
		<TooltipPrimitive.Portal>
			<TooltipPrimitive.Content
				data-slot="tooltip-content"
				sideOffset={sideOffset}
				className={cn(
					"pointer-events-none z-50 w-fit origin-(--radix-tooltip-content-transform-origin) animate-in rounded-md bg-overlay px-3 py-1.5 text-xs text-wrap text-popover-foreground shadow-(--shadow-popover) fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
					keys && "flex items-center gap-2.5",
					className,
				)}
				{...props}
			>
				{keys ? <span>{children}</span> : children}
				{keys ? (
					<span data-slot="tooltip-shortcut" className="inline-flex shrink-0 items-center gap-1 text-muted-foreground" aria-hidden="true">
						{keys.map((key, index) => <kbd key={`${key}-${index}`} className="font-sans">{key}</kbd>)}
					</span>
				) : null}
				{hideArrow ? null : (
					<TooltipPrimitive.Arrow className="z-50 size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-[2px] bg-overlay fill-overlay" />
				)}
			</TooltipPrimitive.Content>
		</TooltipPrimitive.Portal>
	);
}

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider, useShortcutKeys };
