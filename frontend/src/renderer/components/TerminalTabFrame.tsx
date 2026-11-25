import { X } from "@/components/app-icons";
import type { ButtonHTMLAttributes, MouseEvent as ReactMouseEvent, ReactNode, Ref } from "react";
import { cn } from "../lib/utils";
import { Pressable } from "./ui/pressable";

type TerminalTabFrameProps = {
	active: boolean;
	/** Room for an agent's name; `"session"` is the session's own tab, the widest. */
	wide?: boolean | "session";
	/** Optional: without one the label starts at the tab's edge. */
	icon?: ReactNode;
	label: string;
	/**
	 * The tab's control at its right edge, usually a close button. It takes no
	 * room: it sits over the title's end, which fades out beneath it, so a tab
	 * never changes width. It shows on the selected tab, and on hover or focus.
	 */
	action?: ReactNode;
	/** Show the action at rest on unselected tabs too (unsaved dot, Browser pages). */
	actionPinned?: boolean;
	/** Give the action its own room after the title, always showing: the session tab's ⋮. */
	actionInline?: boolean;
	/** Closes the tab; a middle-click runs it, as the close button does. */
	onClose?: () => void;
	buttonRef?: Ref<HTMLButtonElement>;
	buttonProps?: ButtonHTMLAttributes<HTMLButtonElement>;
	/** Replaces the label while the tab is renamed. */
	editingContent?: ReactNode;
	/** The text being edited; it sizes the field so the tab does not jump. */
	editingValue?: string;
};

/** Shows a tab's hover-only control on hover, keyboard focus or a drag; clickable only while shown. */
export const tabHoverRevealClassName =
	"pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 focus-visible-in-group:pointer-events-auto focus-visible-in-group:opacity-100 in-data-dragging:pointer-events-auto in-data-dragging:opacity-100";

/**
 * Fades the end of a tab's title under its control. The control's 16px box sits
 * 8px from the edge: clear for 24px, then a 12px fade.
 */
export const tabFadeClassName = "[mask-image:linear-gradient(to_left,transparent_24px,black_36px)]";

/** The same fade for a hover-only control, with its triggers. */
export const tabHoverFadeClassName =
	"group-hover:[mask-image:linear-gradient(to_left,transparent_24px,black_36px)] focus-visible-in-group:[mask-image:linear-gradient(to_left,transparent_24px,black_36px)] in-data-dragging:[mask-image:linear-gradient(to_left,transparent_24px,black_36px)]";

/** A tab's close button or menu, shared so they match. A pure ghost: hover only brightens the glyph, never a fill. */
export const terminalTabActionClassName =
	"relative grid size-icon-base cursor-pointer place-items-center text-passive before:absolute before:-inset-1.5 before:content-[''] hover:text-foreground focus-visible:text-foreground";

// Laid over the label's own box, growing outward by its padding so the text
// stays exactly where the label drew it. The ring takes no layout space.
export const terminalTabRenameInputClassName =
	"absolute -left-1 -top-0.5 h-[calc(100%+4px)] w-[calc(100%+8px)] rounded-xs bg-background px-1 text-control leading-none text-foreground outline-none ring-1 ring-accent";

/**
 * A tab's ghost close button, for the frame's `action` slot at the tab's
 * right edge. The click stays off the tab, so closing never selects it first.
 */
export function TabCloseButton({ label, onClose }: { label: string; onClose: () => void }) {
	return (
		<Pressable
			aria-label={label}
			data-telemetry-id="tab.close"
			className={terminalTabActionClassName}
			onClick={(event) => {
				event.stopPropagation();
				onClose();
			}}
			onDoubleClick={(event) => event.stopPropagation()}
			type="button"
		>
			<X aria-hidden="true" className="size-icon-sm" />
		</Pressable>
	);
}

// One chrome for every tab strip: the session pane strip (the session itself,
// reviewer, shells, files and PRs), the Browser panel's pages and the side
// chats. Members only supply an icon, a label and the control at the tab's
// right edge, so they cannot drift apart. The floating bar's look (pills,
// dividers) is styles.css's, keyed on data-terminal-tab-frame; a strip other
// than the session's marks its tab list with data-tab-strip for its dividers.
export function TerminalTabFrame({
	active,
	wide = false,
	icon,
	label,
	action,
	actionPinned = false,
	actionInline = false,
	onClose,
	buttonRef,
	buttonProps,
	editingContent,
	editingValue,
}: TerminalTabFrameProps) {
	const slotted = Boolean(action) && actionInline;
	const overlaid = Boolean(action) && !actionInline;
	const shownAtRest = actionPinned || active;
	return (
		<span
			className={cn(
				"group relative inline-flex h-full min-w-shell-tab-min shrink-0 self-stretch items-center border-r border-border",
				wide === "session" ? "max-w-session-tab-max" : wide ? "max-w-agent-shell-tab-max" : "max-w-shell-tab-max",
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
				<span className={cn("flex h-full min-w-0 flex-1 items-center gap-2 text-control leading-none", slotted ? "pl-2 pr-1.5" : "px-2")}>
					{icon ? <span className="grid size-icon-base shrink-0 place-items-center">{icon}</span> : null}
					<span className="relative flex min-w-0 flex-1">
						<span aria-hidden="true" className="invisible min-w-0 truncate whitespace-pre">{editingValue || "\u200b"}</span>
						{editingContent}
					</span>
				</span>
			) : (
				<Pressable
					ref={buttonRef}
					// On the select button only: the rename field and the action slot are not a selection.
					data-telemetry-id="tab.select"
					className={cn(
						"inline-flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 text-control leading-none in-data-dragging:cursor-grabbing!",
						slotted ? "pl-2 pr-1.5" : "px-2",
						overlaid && (shownAtRest ? tabFadeClassName : tabHoverFadeClassName),
					)}
					{...buttonProps}
				>
					{icon ? <span className="pointer-events-none grid size-icon-base shrink-0 place-items-center">{icon}</span> : null}
					<span className="truncate">{label}</span>
				</Pressable>
			)}
			{action ? (
				<span
					className={cn(
						"grid size-icon-base shrink-0 place-items-center",
						slotted ? "mr-2" : cn("absolute right-2 top-1/2 -translate-y-1/2", !shownAtRest && tabHoverRevealClassName),
						// Renaming hides the control (keeping the ⋮'s room, so the tab holds its width).
						editingContent && "invisible",
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
