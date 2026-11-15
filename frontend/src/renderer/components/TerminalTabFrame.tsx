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
	 * The tab's control. At `"icon"` (a close button) it shows on hover or
	 * focus in the icon's slot, or at the tab's right edge when there is no
	 * icon. At `"end"` (the session tab's ⋮) it sits after the title in a slot
	 * of its own, so it never covers the icon.
	 */
	action?: ReactNode;
	actionPosition?: "icon" | "end";
	/** Closes the tab; a middle-click runs it, as the close button does. */
	onClose?: () => void;
	/** Keep the action showing at rest (unsaved dot, running switch). */
	actionPinned?: boolean;
	buttonRef?: Ref<HTMLButtonElement>;
	buttonProps?: ButtonHTMLAttributes<HTMLButtonElement>;
	/** Replaces the label while the tab is renamed. */
	editingContent?: ReactNode;
	/** The text being edited; it sizes the field so the tab does not jump. */
	editingValue?: string;
};

/** A tab's close button or menu, shared so they match. A pure ghost: hover only brightens the glyph, never a fill. */
export const terminalTabActionClassName =
	"relative grid size-icon-base cursor-pointer place-items-center text-passive before:absolute before:-inset-1.5 before:content-[''] hover:text-foreground focus-visible:text-foreground";

// Laid over the label's own box, growing outward by its padding so the text
// stays exactly where the label drew it. The ring takes no layout space.
export const terminalTabRenameInputClassName =
	"absolute -left-1 -top-0.5 h-[calc(100%+4px)] w-[calc(100%+8px)] rounded-xs bg-background px-1 text-control leading-none text-foreground outline-none ring-1 ring-accent";

/**
 * A tab's ghost close button, for the frame's `action` slot: over the icon on
 * hover (`actionPosition="icon"`), or at the tab's end (`"end"`, pinned to show
 * at rest). The click stays off the tab, so closing never selects it first.
 */
export function TabCloseButton({ label, onClose }: { label: string; onClose: () => void }) {
	return (
		<Pressable
			aria-label={label}
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
// reviewer, shells and files), the Browser panel's pages and the side chats.
// Members only supply an icon, a label and the control that replaces the icon
// on hover, so they cannot drift apart. The floating bar's look (pills,
// dividers) is styles.css's, keyed on data-terminal-tab-frame; a strip other
// than the session's marks its tab list with data-tab-strip for its dividers.
export function TerminalTabFrame({
	active,
	wide = false,
	icon,
	label,
	action,
	actionPosition = "icon",
	actionPinned = false,
	onClose,
	buttonRef,
	buttonProps,
	editingContent,
	editingValue,
}: TerminalTabFrameProps) {
	const iconAction = actionPosition === "icon" ? action : undefined;
	const endAction = actionPosition === "end" ? action : undefined;
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
				<span className={cn("flex h-full min-w-0 flex-1 items-center gap-2 text-control leading-none", endAction ? "pl-2 pr-1.5" : "px-2")}>
					{icon ? <span className="grid size-icon-base shrink-0 place-items-center">{icon}</span> : null}
					<span className="relative flex min-w-0 flex-1">
						<span aria-hidden="true" className="invisible min-w-0 truncate whitespace-pre">{editingValue || "\u200b"}</span>
						{editingContent}
					</span>
				</span>
			) : (
				<Pressable
					ref={buttonRef}
					className={cn(
						"inline-flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 text-control leading-none in-data-dragging:cursor-grabbing!",
						endAction ? "pl-2 pr-1.5" : "px-2",
						// Without an icon the action takes the tab's right edge; keep the label clear of it.
						!icon && iconAction && "pr-7",
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
								iconAction && (actionPinned ? "opacity-0" : "group-hover:opacity-0 group-has-[:focus-visible]:opacity-0 [[data-terminal-tab-frame]:has([data-state=open])>button>&]:opacity-0 in-data-dragging:opacity-0"),
							)}
						>
							{icon}
						</span>
					) : null}
					<span className="truncate">{label}</span>
				</Pressable>
			)}
			{endAction ? (
				<span
					className={cn(
						"mr-2 grid size-icon-base shrink-0 place-items-center",
						actionPinned
							? undefined
							: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-has-[:focus-visible]:pointer-events-auto group-has-[:focus-visible]:opacity-100 has-[[data-state=open]]:pointer-events-auto has-[[data-state=open]]:opacity-100",
					)}
					data-terminal-tab-action
				>
					{endAction}
				</span>
			) : null}
			{iconAction ? (
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
					{iconAction}
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
