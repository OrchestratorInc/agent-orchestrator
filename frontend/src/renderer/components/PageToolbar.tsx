import { useRef, useState } from "react";
import { Search, X } from "@/components/app-icons";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * Search behind an icon for a page's filter row; the field shows while
 * focused or holding a query. Escape clears it and returns focus to the icon.
 */
export function ToolbarSearch({ label, query, onQueryChange }: { label: string; query: string; onQueryChange: (query: string) => void }) {
	const [open, setOpen] = useState(false);
	const toggleRef = useRef<HTMLButtonElement>(null);
	if (open || query) {
		return (
			<label className="relative me-1 block w-56 min-w-0 shrink">
				<Search aria-hidden="true" className="pointer-events-none absolute start-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
				<Input
					aria-label={label}
					autoFocus
					className="inspector-field-input ps-8 [&::-webkit-search-cancel-button]:appearance-none"
					onBlur={() => setOpen(false)}
					onChange={(event) => onQueryChange(event.target.value)}
					onKeyDown={(event) => {
						if (event.key !== "Escape") return;
						// Escape clears search first; a later one reaches the page.
						event.preventDefault();
						onQueryChange("");
						setOpen(false);
						requestAnimationFrame(() => toggleRef.current?.focus());
					}}
					placeholder={label}
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
export function FilterChip({ label, value, removeLabel, onRemove }: { label: string; value: string; removeLabel: string; onRemove: () => void }) {
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
			<span className="text-muted-foreground">{label}:</span>
			<span className="truncate">{value}</span>
			<X aria-hidden="true" className="size-icon-xs text-muted-foreground" />
		</Button>
	);
}
