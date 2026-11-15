import { useEffect, useRef } from "react";
import { CornerDownLeft } from "@/components/app-icons";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { cn } from "../../lib/utils";
import { Pressable } from "../ui/pressable";
import { composerFileIcon } from "./composerFileIcon";
import type { Suggestion, TriggerKind } from "./composerSuggest";

export function ComposerSuggestMenu({
	id,
	kind,
	items,
	highlighted,
	onPick,
	truncated,
}: {
	id: string;
	kind: TriggerKind;
	items: Suggestion[];
	highlighted: number;
	onPick: (value: string) => void;
	truncated?: boolean;
}) {
	const list = useRef<HTMLUListElement>(null);
	const previousHighlighted = useRef(highlighted);
	useEdgeFade(list, "y", [items.length === 0]);

	useEffect(() => {
		const previous = previousHighlighted.current;
		previousHighlighted.current = highlighted;
		if (highlighted === 0 && previous === 0 && (list.current?.scrollTop ?? 0) <= 1) return;
		const row = list.current?.querySelector<HTMLElement>(`[data-index="${highlighted}"]`);
		row?.scrollIntoView({ block: "nearest" });
	}, [highlighted, items]);

	if (items.length === 0) return null;

	return (
		<div
			className="absolute bottom-full left-0 z-overlay mb-1.5 flex w-full max-w-md flex-col overflow-hidden rounded-lg border border-(--color-border-overlay) bg-overlay p-1 text-popover-foreground shadow-(--shadow-popover)"
			onMouseDown={(event) => event.preventDefault()}
		>
			<ul
				ref={list}
				id={id}
				role="listbox"
				className="model-menu-scroll edge-fade flex max-h-64 scroll-py-4 flex-col overflow-y-auto overscroll-contain [--edge-fade-size:16px]"
			>
				{items.map((item, index) => {
					const active = index === highlighted;
					const badge = displayBadge(item.badge);
					const Icon = kind === "file" ? composerFileIcon(item.value) : undefined;
					return (
						<li key={item.value} data-index={index}>
							<Pressable
								tabIndex={-1}
								role="option"
								id={`${id}-option-${index}`}
								aria-selected={active}
								onClick={() => onPick(item.value)}
								className={cn(
									"flex h-8 w-full items-center gap-2 rounded-sm px-2 text-left text-xs text-muted-foreground outline-none !transition-none",
									active
										? "bg-interactive-active text-foreground"
										: "bg-transparent hover:bg-interactive-hover hover:text-foreground",
								)}
							>
								{Icon ? (
									<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
								) : null}
								<span className="flex min-w-0 flex-1 items-baseline gap-2">
									<span
										className={cn(
											"truncate",
											item.detail ? "max-w-3/5 shrink-0" : "min-w-0",
											kind === "skill" && "text-foreground",
										)}
									>
										{item.label}
									</span>
									{item.detail ? (
										<span
											className={cn(
												"min-w-0 flex-1 truncate text-muted-foreground",
												kind === "file" && "text-caption",
											)}
										>
											{item.detail}
										</span>
									) : null}
								</span>
								{badge ? <span className="shrink-0 text-micro text-muted-foreground">{badge}</span> : null}
								{active ? (
									<span
										aria-label="Press Tab or Enter to insert"
										className="flex shrink-0 items-center text-muted-foreground"
									>
										<CornerDownLeft aria-hidden="true" className="size-3" />
									</span>
								) : null}
							</Pressable>
						</li>
					);
				})}
			</ul>

			{truncated ? (
				<p className="mt-1 border-t border-border px-2 pt-1.5 pb-0.5 text-micro text-muted-foreground">
					Showing part of a large worktree — type more to narrow it.
				</p>
			) : null}
		</div>
	);
}

function displayBadge(badge?: string): string | undefined {
	if (!badge || badge.toLowerCase() === "agent") return undefined;
	return badge.toLowerCase() === "ao" ? "Orchestrator" : badge;
}
