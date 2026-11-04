import { cn } from "../../lib/utils";

/**
 * Keys as small caps (⇧ ⌘ O), used in the Cmd+K footer.
 */
function ShortcutCaps({ keys, className }: { keys: readonly string[]; className?: string }) {
	return (
		<span data-slot="shortcut-caps" className={cn("inline-flex shrink-0 items-center gap-1", className)} aria-hidden="true">
			{keys.map((key, index) => (
				<kbd
					key={`${key}-${index}`}
					className="inline-flex h-control-xs min-w-control-xs items-center justify-center rounded-sm bg-interactive-active px-1.5 font-sans text-caption leading-none text-foreground"
				>
					{key}
				</kbd>
			))}
		</span>
	);
}

export { ShortcutCaps };
