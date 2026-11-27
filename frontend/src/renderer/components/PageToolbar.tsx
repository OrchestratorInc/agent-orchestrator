import { useRef, useState, type RefObject } from "react";
import { Search, X } from "@/components/app-icons";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * Search behind an icon for a page's filter row; the field shows while
 * focused or holding a query. Escape clears it and returns focus to the icon.
 * `persistent` keeps the field always shown (no icon, no autofocus): Escape
 * clears the text, and a second Escape leaves the field.
 */
export function ToolbarSearch({ label, query, onQueryChange, persistent = false, inputRef }: { label: string; query: string; onQueryChange: (query: string) => void; persistent?: boolean; inputRef?: RefObject<HTMLInputElement | null> }) {
	const [open, setOpen] = useState(false);
	const toggleRef = useRef<HTMLButtonElement>(null);
	if (persistent || open || query) {
		return (
			<label className="relative block w-56 min-w-0 shrink">
				<Search aria-hidden="true" className="pointer-events-none absolute start-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
				<Input
					aria-label={label}
					autoFocus={!persistent}
					className="inspector-field-input ps-8 [&::-webkit-search-cancel-button]:appearance-none"
					onBlur={persistent ? undefined : () => setOpen(false)}
					onChange={(event) => onQueryChange(event.target.value)}
					onKeyDown={(event) => {
						if (event.key !== "Escape") return;
						if (persistent && !query) {
							event.currentTarget.blur();
							return;
						}
						// Escape clears search first; a later one reaches the page.
						event.preventDefault();
						onQueryChange("");
						if (persistent) return;
						setOpen(false);
						requestAnimationFrame(() => toggleRef.current?.focus());
					}}
					placeholder={label}
					ref={inputRef}
					type="search"
					value={query}
				/>
			</label>
		);
	}
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button ref={toggleRef} type="button" size="icon-sm" variant="quiet" aria-label={label} onClick={() => setOpen(true)}>
					<Search aria-hidden="true" className="size-icon-sm" />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}

/** One active filter from a page's Filter menu, removable in place. */
export function FilterChip({ label, value, removeLabel, onRemove }: { label: string; value?: string; removeLabel: string; onRemove: () => void }) {
	return (
		<Button
			type="button"
			size="xs"
			variant="secondary"
			shape="pill"
			className="max-w-64 ps-2.5 pe-1.5"
			aria-label={removeLabel}
			onClick={onRemove}
		>
			{value === undefined ? <span className="truncate">{label}</span> : (
				<>
					<span className="text-muted-foreground">{label}:</span>
					<span className="truncate">{value}</span>
				</>
			)}
			<X aria-hidden="true" className="size-icon-xs text-muted-foreground" />
		</Button>
	);
}
