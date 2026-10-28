import type { ButtonHTMLAttributes, MouseEvent as ReactMouseEvent, ReactNode, Ref } from "react";
import { cn } from "../lib/utils";

type TerminalTabFrameProps = {
	active: boolean;
	icon: ReactNode;
	label: string;
	/** Takes the icon's slot on hover or focus: the tab's close button or menu. */
	action?: ReactNode;
	/** Keep the action in the icon's slot at rest (unsaved dot, running switch). */
	actionPinned?: boolean;
	buttonRef?: Ref<HTMLButtonElement>;
	buttonProps?: ButtonHTMLAttributes<HTMLButtonElement>;
	/** Replaces the label while the tab is renamed. */
	editingContent?: ReactNode;
};

/** The control in a tab's icon slot, shared so close buttons and menus match. */
export const terminalTabActionClassName =
	"relative grid size-icon-base cursor-pointer place-items-center rounded-sm text-passive before:absolute before:-inset-1.5 before:content-[''] hover:bg-interactive-hover hover:text-foreground focus-visible:bg-interactive-hover focus-visible:text-foreground";

// One chrome for every tab in the session pane strip: the session itself,
// reviewer, shells and files. Members only supply an icon, a label and the
// control that replaces the icon on hover, so they cannot drift apart.
export function TerminalTabFrame({
	active,
	icon,
	label,
	action,
	actionPinned = false,
	buttonRef,
	buttonProps,
	editingContent,
}: TerminalTabFrameProps) {
	return (
		<span
			className={cn(
				"group relative inline-flex h-full min-w-shell-tab-min max-w-shell-tab-max shrink-0 self-stretch items-center border-r border-border",
				// Focus rings are off app-wide, so keyboard focus shows as the hover wash.
				active
					? "bg-overlay text-foreground"
					: "text-passive hover:bg-raised hover:text-foreground has-[:focus-visible]:bg-raised has-[:focus-visible]:text-foreground in-data-dragging:bg-raised in-data-dragging:text-foreground",
			)}
			data-terminal-tab-frame
			onClick={(event) => {
				if (event.target !== event.currentTarget) return;
				buttonProps?.onClick?.(event as unknown as ReactMouseEvent<HTMLButtonElement>);
			}}
		>
			{editingContent ? (
				<span className="flex h-full min-w-0 flex-1 items-center gap-2 px-2">
					<span className="grid size-icon-base shrink-0 place-items-center">{icon}</span>
					{editingContent}
				</span>
			) : (
				<button
					ref={buttonRef}
					className="inline-flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 px-2 text-left text-control leading-none in-data-dragging:cursor-grabbing!"
					{...buttonProps}
				>
					<span
						className={cn(
							"pointer-events-none grid size-icon-base shrink-0 place-items-center",
							action && (actionPinned ? "opacity-0" : "group-hover:opacity-0 group-focus-within:opacity-0 group-has-[[data-state=open]]:opacity-0 in-data-dragging:opacity-0"),
						)}
					>
						{icon}
					</span>
					<span className="truncate">{label}</span>
				</button>
			)}
			{action ? (
				<span
					className={cn(
						"absolute left-2 top-1/2 grid size-icon-base -translate-y-1/2 place-items-center",
						// Clickable only while revealed: a tap or a click on a faded slot
						// selects the tab (the icon lets it through) instead of closing it.
						actionPinned
							? undefined
							: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 has-[[data-state=open]]:pointer-events-auto has-[[data-state=open]]:opacity-100 in-data-dragging:pointer-events-auto in-data-dragging:opacity-100",
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
