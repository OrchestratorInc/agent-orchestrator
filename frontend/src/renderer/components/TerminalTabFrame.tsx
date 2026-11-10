import type { ButtonHTMLAttributes, MouseEvent as ReactMouseEvent, ReactNode, Ref } from "react";
import { cn } from "../lib/utils";
import { Pressable } from "./ui/pressable";

type TerminalTabFrameProps = {
	active: boolean;
	wide?: boolean;
	/** Optional: without one the label starts at the tab's edge. */
	icon?: ReactNode;
	label: string;
	/** Shown on hover or focus: the tab's ⋮ menu, or a running interface switch's status on the session's tab. It takes the icon's slot, or the tab's right edge when there is no icon. */
	action?: ReactNode;
	/** Closes the tab; a middle-click runs it, as the menu's Close tab does. */
	onClose?: () => void;
	/** Keep the action showing at rest (unsaved dot, running switch). */
	actionPinned?: boolean;
	buttonRef?: Ref<HTMLButtonElement>;
	buttonProps?: ButtonHTMLAttributes<HTMLButtonElement>;
	/** Replaces the label while the tab is renamed. */
	editingContent?: ReactNode;
	/** The text being edited; it sizes the field so the tab does not jump. */
	editingValue?: string;
	/** A status glyph after the label, e.g. the live state of an agent in a shell. */
	trailing?: ReactNode;
};

/** The control in a tab's icon slot, shared so every tab's ⋮ button matches. */
export const terminalTabActionClassName =
	"relative grid size-icon-base cursor-pointer place-items-center rounded-sm text-passive before:absolute before:-inset-1.5 before:content-[''] hover:bg-interactive-hover hover:text-foreground focus-visible:bg-interactive-hover focus-visible:text-foreground";

// Laid over the label's own box, growing outward by its padding so the text
// stays exactly where the label drew it. The ring takes no layout space.
export const terminalTabRenameInputClassName =
	"absolute -left-1 -top-0.5 h-[calc(100%+4px)] w-[calc(100%+8px)] rounded-xs bg-background px-1 text-control leading-none text-foreground outline-none ring-1 ring-accent";

// One chrome for every tab in the session pane strip: the session itself,
// reviewer, shells and files. Members only supply an icon, a label and the
// control that replaces the icon on hover, so they cannot drift apart.
export function TerminalTabFrame({
	active,
	wide = false,
	icon,
	label,
	action,
	actionPinned = false,
	onClose,
	buttonRef,
	buttonProps,
	editingContent,
	editingValue,
	trailing,
}: TerminalTabFrameProps) {
	return (
		<span
			className={cn(
				"group relative inline-flex h-full min-w-shell-tab-min shrink-0 self-stretch items-center border-r border-border",
				wide ? "max-w-agent-shell-tab-max" : "max-w-shell-tab-max",
				// Focus rings are off app-wide, so keyboard focus shows as the hover wash.
				active
					? "bg-overlay text-foreground"
					: "text-passive hover:bg-raised hover:text-foreground has-[:focus-visible]:bg-raised has-[:focus-visible]:text-foreground in-data-dragging:bg-raised in-data-dragging:text-foreground",
			)}
			data-active={active || undefined}
			data-terminal-tab-frame
			onClick={(event) => {
				if (event.target !== event.currentTarget) return;
				buttonProps?.onClick?.(event as unknown as ReactMouseEvent<HTMLButtonElement>);
			}}
			// No autoscroll or selection paste on the press; the release closes.
			onMouseDown={onClose ? (event) => { if (event.button === 1) event.preventDefault(); } : undefined}
			onAuxClick={onClose ? (event) => {
				if (event.button !== 1) return;
				event.preventDefault();
				onClose();
			} : undefined}
		>
			{editingContent ? (
				<span className="flex h-full min-w-0 flex-1 items-center gap-2 px-2 text-control leading-none">
					{icon ? <span className="grid size-icon-base shrink-0 place-items-center">{icon}</span> : null}
					<span className="relative flex min-w-0 flex-1">
						<span aria-hidden="true" className="invisible min-w-0 truncate whitespace-pre">{editingValue || "\u200b"}</span>
						{editingContent}
					</span>
					{trailing ? <span className="grid shrink-0 place-items-center">{trailing}</span> : null}
				</span>
			) : (
				<Pressable
					ref={buttonRef}
					className={cn(
						"inline-flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 px-2 text-control leading-none in-data-dragging:cursor-grabbing!",
						// Without an icon the action takes the tab's right edge; keep the label clear of it.
						!icon && action && "pr-7",
					)}
					{...buttonProps}
				>
					{/* The open-menu rule reaches the icon through child combinators. The
					    group-has-[[data-state=open]] form (`:has([attr]) *`) made Chromium
					    restyle the whole document on every DOM change in the app. */}
					{icon ? (
						<span
							className={cn(
								"pointer-events-none grid size-icon-base shrink-0 place-items-center",
								action && (actionPinned ? "opacity-0" : "group-hover:opacity-0 group-has-[:focus-visible]:opacity-0 [[data-terminal-tab-frame]:has([data-state=open])>button>&]:opacity-0 in-data-dragging:opacity-0"),
							)}
						>
							{icon}
						</span>
					) : null}
					<span className="truncate">{label}</span>
					{trailing ? <span className="grid shrink-0 place-items-center">{trailing}</span> : null}
				</Pressable>
			)}
			{action ? (
				<span
					className={cn(
						"absolute top-1/2 grid size-icon-base -translate-y-1/2 place-items-center",
						icon ? "left-2" : "right-2",
						// Clickable only while revealed: a tap or a click on a faded slot
						// selects the tab (the icon lets it through) instead of closing it.
						actionPinned
							? undefined
							: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-has-[:focus-visible]:pointer-events-auto group-has-[:focus-visible]:opacity-100 has-[[data-state=open]]:pointer-events-auto has-[[data-state=open]]:opacity-100 in-data-dragging:pointer-events-auto in-data-dragging:opacity-100",
					)}
					data-terminal-tab-action
				>
					{action}
				</span>
			) : null}
			{active ? (
				<span
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 bottom-0 h-0.5 bg-foreground/80"
					data-testid="active-terminal-tab-indicator"
				/>
			) : null}
		</span>
	);
}
