import { cn } from "../../lib/utils";

/**
 * Keys as small caps (⇧ ⌘ O). The one visual for a keybinding outside the
 * plain-glyph hint in command rows; the Cmd+K footer and tooltips share it.
 */
function ShortcutCaps({ keys, className, capClassName }: { keys: readonly string[]; className?: string; capClassName?: string }) {
	return (
		<span data-slot="shortcut-caps" className={cn("inline-flex shrink-0 items-center gap-1", className)} aria-hidden="true">
			{keys.map((key, index) => (
				<kbd
					key={`${key}-${index}`}
					className={cn(
						"inline-flex h-control-xs min-w-control-xs items-center justify-center rounded-sm bg-interactive-active px-1.5 font-sans text-caption leading-none text-foreground",
						capClassName,
					)}
				>
					{key}
				</kbd>
			))}
		</span>
	);
}

export { ShortcutCaps };
