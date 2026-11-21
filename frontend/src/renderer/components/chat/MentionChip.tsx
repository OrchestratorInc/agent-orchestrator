import { createContext, Fragment, useContext, type ComponentProps, type ReactNode } from "react";
import { splitByMentions } from "../../lib/cloud-mentions";
import { cn } from "../../lib/utils";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";

/**
 * Who reads the timeline, so a chip that names them, or a team that reached
 * them, is tinted. The chat surface of a shared Cloud session provides it.
 */
export interface MentionTint {
	userId?: string;
	teamIds?: ReadonlySet<string>;
}

export const MentionTintContext = createContext<MentionTint>({});

/**
 * A person or team named in text: their mark and name on a quiet pill. The
 * composer draws its chips this way, and a sent message draws its mentions
 * the same way, so a mention reads the same before and after sending. A chip
 * naming the reader (or a team that reached them) is tinted.
 */
export function MentionChip({ kind, name, tinted, className, ...props }: { kind: "person" | "team"; name: string; tinted?: boolean } & ComponentProps<"span">) {
	return (
		<span
			className={cn(
				"mx-0.5 inline-flex items-center gap-1 rounded-md py-0.5 pr-1.5 pl-0.5 align-middle text-[0.9em] leading-none text-foreground",
				tinted ? "bg-accent-weak" : "bg-interactive-hover",
				className,
			)}
			data-mention-chip={kind}
			data-mention-self={tinted ? "" : undefined}
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
	mentions: readonly { kind: "person" | "team"; id: string; name: string }[];
	renderText?: (text: string) => ReactNode;
}) {
	const tint = useContext(MentionTintContext);
	return splitByMentions(text, mentions).map((part, index) => typeof part === "string"
		? <Fragment key={index}>{renderText ? renderText(part) : part}</Fragment>
		: (
			<MentionChip
				key={index}
				kind={part.kind}
				name={part.name}
				tinted={part.kind === "person" ? part.id === tint.userId : tint.teamIds?.has(part.id) === true}
			/>
		));
}
