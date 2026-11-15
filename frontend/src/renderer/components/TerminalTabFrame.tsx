import { X } from "@/components/app-icons";
import type { ButtonHTMLAttributes, MouseEvent as ReactMouseEvent, ReactNode, Ref } from "react";
import { cn } from "../lib/utils";
import { Pressable } from "./ui/pressable";

type TerminalTabFrameProps = {
	active: boolean;
	wide?: boolean | "session";
	icon?: ReactNode;
	label: string;
	action?: ReactNode;
	actionPosition?: "icon" | "end";
	onClose?: () => void;
	actionPinned?: boolean;
	buttonRef?: Ref<HTMLButtonElement>;
	buttonProps?: ButtonHTMLAttributes<HTMLButtonElement>;
	editingContent?: ReactNode;
	editingValue?: string;
};

export const terminalTabActionClassName =
	"relative grid size-icon-base cursor-pointer place-items-center text-passive before:absolute before:-inset-1.5 before:content-[''] hover:text-foreground focus-visible:text-foreground";

export const terminalTabRenameInputClassName =
	"absolute -left-1 -top-0.5 h-[calc(100%+4px)] w-[calc(100%+8px)] rounded-xs bg-background px-1 text-control leading-none text-foreground outline-none ring-1 ring-accent";

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
						!icon && iconAction && "pr-7",
					)}
					{...buttonProps}
				>
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
