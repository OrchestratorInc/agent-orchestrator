import { Fragment, type ComponentProps, type ReactNode } from "react";
import { splitByMentions } from "../../lib/cloud-team-collab";
import { cn } from "../../lib/utils";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";

/**
 * A person or team named in text: their mark and name on a quiet pill. The
 * composer draws its chips this way, and a posted message or aside draws its
 * mentions the same way, so a mention reads the same before and after sending.
 */
export function MentionChip({ kind, name, className, ...props }: { kind: "person" | "team"; name: string } & ComponentProps<"span">) {
	return (
		<span
			className={cn("mx-0.5 inline-flex items-center gap-1 rounded-md bg-interactive-hover py-0.5 pr-1.5 pl-0.5 align-middle text-[0.9em] leading-none text-foreground", className)}
			data-mention-chip={kind}
			{...props}
		>
			{kind === "person" ? <PersonAvatar name={name} size="xs" /> : <TeamTile name={name} size="sm" className="size-4 rounded-[4px] text-[7px]" />}
			{name}
		</span>
	);
}

/** Text with the people and teams it names drawn as chips; the rest as `renderText` draws it. */
export function MentionedText({ text, mentions, renderText }: {
	text: string;
	mentions: readonly { kind: "person" | "team"; name: string }[];
	renderText?: (text: string) => ReactNode;
}) {
	return splitByMentions(text, mentions).map((part, index) => typeof part === "string"
		? <Fragment key={index}>{renderText ? renderText(part) : part}</Fragment>
		: <MentionChip key={index} kind={part.kind} name={part.name} />);
}
