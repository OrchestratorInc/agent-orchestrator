/**
 * The frame every block of code or output in the chat shares, and its header.
 */

import type { HTMLAttributes, ReactNode } from "react";
import { CopyButton } from "./CopyButton";

/**
 * The frame every box in a step's details shares: the chat's own canvas behind a
 * faint outline, so a box reads as part of the conversation rather than a panel
 * laid on it. The Shell box, a tool's arguments and output, code blocks and
 * diagrams all use it.
 */
export const CHAT_BOX_CLASS =
	"chat-code mt-1 mb-1 flex min-w-0 flex-col overflow-hidden rounded-[var(--radius-panel)] border border-border bg-background";

/**
 * A box's header: its mark, its label, anything the box adds, and copy. The mark
 * and the copy icon are one size, so the row reads as one line. Every button here
 * is always shown, never only on hover.
 */
export function ChatBoxHeader({
	icon,
	label,
	title,
	extra,
	copyText,
	copyLabel,
	...rest
}: {
	icon?: ReactNode;
	label: ReactNode;
	title?: string;
	extra?: ReactNode;
	copyText?: string | (() => string);
	copyLabel?: string;
} & Omit<HTMLAttributes<HTMLDivElement>, "title">) {
	return (
		<div {...rest} data-chat-box-header="" className="flex min-h-10 min-w-0 items-center gap-2.5 pr-2 pl-3 pt-1">
			{icon}
			<span className="flex min-w-0 flex-1 text-control leading-5 text-muted-foreground" title={title}>
				{label}
			</span>
			{extra}
			{copyText !== undefined && copyLabel ? (
				<CopyButton compact size="icon-xs" text={copyText} label={copyLabel} className="[&_svg]:size-3.5" />
			) : null}
		</div>
	);
}
