import { createContext, Fragment, useContext, type ClipboardEvent, type HTMLAttributes, type ReactNode } from "react";
import { Box } from "@/components/app-icons";
import type { ChatSkill } from "../../types/conversation";
import { cn } from "../../lib/utils";
import { composerFileIcon } from "./composerFileIcon";
import { skillToken, splitComposerTokens, useSentComposerTokens, type ComposerSentToken } from "./sentComposerTokens";

/**
 * A skill or file chip: the composer's inline token, and the same token in the
 * message it sent. In the composer it is an atom the caret skips; in a sent
 * message its text stays selectable, and copying gives back the wire text.
 */
export function ComposerTokenChip({
	kind,
	value,
	display,
	wire,
	selectable = false,
	className,
	...props
}: ComposerSentToken & { selectable?: boolean } & HTMLAttributes<HTMLSpanElement>) {
	const Icon = kind === "skill" ? Box : composerFileIcon(value);
	return (
		<span
			{...props}
			data-composer-token={kind}
			data-value={value}
			data-token-wire={selectable ? wire : undefined}
			title={kind === "file" ? value : undefined}
			className={cn(
				"mx-0.5 inline-flex items-center gap-1 rounded-md border border-border-strong bg-interactive-hover px-1.5 py-0.5 align-middle text-[0.9em] leading-none",
				selectable ? "whitespace-nowrap" : "select-none",
				kind === "skill" ? "text-logo-accent" : "text-foreground",
				className,
			)}
		>
			<Icon aria-hidden="true" className="size-3 shrink-0" />
			{display}
		</span>
	);
}

/** The skills the session's agent lists, so a sent `/name` can show as its chip. */
export const ChatTokenSkills = createContext<readonly ChatSkill[]>([]);

/**
 * A sent message's text with its skill and file chips drawn as the composer drew
 * them. Only exact wires count: a skill the session lists, or a chip this app
 * recorded when the message was sent. Everything else, code included, is text.
 * It works on one run of inline text, so a Markdown renderer can call it per text
 * node (list items included) and leave code elements alone.
 */
export function ComposerTokenText({
	text,
	scopes,
	renderText,
}: {
	text: string;
	/** Where the send recorded its chips; see sessionTokenScope and messageTokenScope. */
	scopes: string[];
	renderText: (text: string) => ReactNode;
}) {
	const skills = useContext(ChatTokenSkills);
	const recorded = useSentComposerTokens(...scopes);
	const tokens = skills.length > 0 ? [...recorded, ...skills.map(skillToken)] : recorded;
	const segments = splitComposerTokens(text, tokens);
	if (segments.length === 1 && !segments[0]?.token) return <>{renderText(text)}</>;
	return segments.map((segment, index) =>
		segment.token ? (
			<ComposerTokenChip key={index} {...segment.token} selectable />
		) : (
			<Fragment key={index}>{renderText(segment.text)}</Fragment>
		),
	);
}

/**
 * Copying across a file chip would give its short name; this puts the text the
 * message actually holds on the clipboard instead. Selections without a chip
 * copy as the browser would.
 */
export function copyTokenWires(event: ClipboardEvent<HTMLElement>): void {
	const selection = globalThis.getSelection?.();
	if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return;
	const fragment = selection.getRangeAt(0).cloneContents();
	const chips = fragment.querySelectorAll<HTMLElement>("[data-token-wire]");
	if (chips.length === 0) return;
	for (const chip of chips) chip.replaceWith(chip.dataset.tokenWire ?? chip.textContent ?? "");
	event.clipboardData.setData("text/plain", fragment.textContent ?? "");
	event.preventDefault();
}
