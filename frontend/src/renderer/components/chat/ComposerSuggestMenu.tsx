/**
 * The composer's suggestion list.
 *
 * Presentation only: it renders ranked rows and reports clicks. Which rows exist,
 * which is highlighted, and what a selection inserts are all decided by the
 * composer, so the keyboard and the mouse cannot disagree about what is selected.
 *
 * Not a Popover or a Command: both want focus. The editor must keep it — the user
 * is still typing, and every keystroke has to reach the field and re-filter the
 * list. So this is a plain positioned panel wired to the editor's own key
 * handling, following the listbox pattern (`aria-activedescendant` on the input
 * rather than a focus move).
 */

import { useEffect, useRef } from "react";
import { CornerDownLeft } from "@/components/app-icons";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { cn } from "../../lib/utils";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";
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
	/** Shared with the editor's `aria-controls`, so the pairing is announced. */
	id: string;
	kind: TriggerKind;
	items: Suggestion[];
	highlighted: number;
	onPick: (value: string) => void;
	/** The candidate list was capped by the daemon, so it is not exhaustive. */
	truncated?: boolean;
}) {
	const list = useRef<HTMLUListElement>(null);
	const previousHighlighted = useRef(highlighted);
	// The list unmounts while nothing matches, so the fade reattaches when it returns.
	useEdgeFade(list, "y", [items.length === 0]);

	// Keep the highlighted row visible when it moves by keyboard past the edge of
	// the scroll area.
	useEffect(() => {
		const previous = previousHighlighted.current;
		previousHighlighted.current = highlighted;
		// Index zero is visible whenever the menu opens or its results re-rank.
		// Calling scrollIntoView for it forced a synchronous layout on every typed
		// character, which made `/` and `@` suggestions hitch the composer. It
		// still needs a scroll when filtering reset a keyboard selection to zero.
		if (highlighted === 0 && previous === 0 && (list.current?.scrollTop ?? 0) <= 1) return;
		const row = list.current?.querySelector<HTMLElement>(`[data-index="${highlighted}"]`);
		row?.scrollIntoView({ block: "nearest" });
	}, [highlighted, items]);

	if (items.length === 0) return null;

	const renderOption = (item: Suggestion, index: number) => {
		const active = index === highlighted;
		const badge = displayBadge(item.badge);
		const Icon = !item.mention && kind === "file" ? composerFileIcon(item.value) : undefined;
		return (
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
				{item.mention ? (
					<span aria-hidden="true" className="flex shrink-0 items-center">
						{item.mention.kind === "person"
							? <PersonAvatar name={item.mention.name} size="xs" />
							: <TeamTile name={item.mention.name} size="sm" className="size-4 rounded-[4px] text-[7px]" />}
					</span>
				) : Icon ? (
					<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
				) : null}
				{/* Name first, then its description or folder on the same line,
				    in smaller text, so a row stays one line tall. */}
				<span className="flex min-w-0 flex-1 items-baseline gap-2">
					<span className={cn("truncate", item.detail ? "max-w-3/5 shrink-0" : "min-w-0")}>
						{kind === "skill" ? `/${item.label}` : item.label}
					</span>
					{item.detail ? (
						<span className="min-w-0 flex-1 truncate text-caption text-muted-foreground">
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
		);
	};

	return (
		<div
			className="absolute bottom-full left-0 z-overlay mb-1.5 flex w-full max-w-md flex-col overflow-hidden rounded-lg border border-(--color-border-overlay) bg-overlay p-1 text-popover-foreground shadow-(--shadow-popover)"
			// The composer owns the keyboard; a pointerdown here must not pull focus
			// out of the textarea on its way to the click.
			onMouseDown={(event) => event.preventDefault()}
		>
			<ul
				ref={list}
				id={id}
				role="listbox"
				// Rows are 32px, so the edge fade is short enough to leave a row readable.
				// The scroll padding keeps a keyboard-highlighted row clear of it.
				className="model-menu-scroll edge-fade flex max-h-64 scroll-py-4 flex-col overflow-y-auto overscroll-contain [--edge-fade-size:16px]"
			>
				{menuGroups(items).map((group) => group.section ? (
					// A section is a group named for screen readers; its visible title says the same.
					<li key={`section:${group.start}`} role="presentation">
						<ul aria-label={group.section} className="flex flex-col" role="group">
							<li
								aria-hidden="true"
								className={cn("px-2 pt-1.5 pb-1 text-caption text-muted-foreground", group.start > 0 && "mt-1")}
								data-suggest-section=""
								role="presentation"
							>
								{group.section}
							</li>
							{group.items.map((item, offset) => (
								<li key={item.value} data-index={group.start + offset} role="presentation">
									{renderOption(item, group.start + offset)}
								</li>
							))}
						</ul>
					</li>
				) : group.items.map((item, offset) => (
					<li key={item.value} data-index={group.start + offset} role="presentation">
						{renderOption(item, group.start + offset)}
					</li>
				)))}
			</ul>

			{truncated ? (
				// Said out loud rather than letting a capped list read as the whole
				// worktree: a file that is missing because of the cap looks identical to
				// one that does not exist.
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

/** Runs of items under one section title (none for an untitled run), each with where it starts. */
function menuGroups<T extends { section?: string }>(items: readonly T[]): Array<{ section?: string; start: number; items: T[] }> {
	const groups: Array<{ section?: string; start: number; items: T[] }> = [];
	items.forEach((item, index) => {
		const last = groups.at(-1);
		if (last && last.section === item.section) last.items.push(item);
		else groups.push({ section: item.section, start: index, items: [item] });
	});
	return groups;
}
