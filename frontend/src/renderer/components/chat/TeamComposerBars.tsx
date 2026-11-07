import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { CloudCpAsideSuggestion } from "../../lib/cloud-cp/types";
import type { ComposerMention } from "../../lib/cloud-team-collab";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";
import { Button } from "../ui/button";

/** One strip above the composer: a quiet surface, one line, its actions at the end. */
const BAR_CLASS = "flex min-w-0 items-center gap-2 rounded-(--radius-chat-composer) bg-surface px-3 py-2 text-xs";

/** Names joined the way the language joins them: "Cara Diaz and Farah Nasser". */
export function joinNames(names: readonly string[], locale: string): string {
	try {
		return new Intl.ListFormat(locale, { style: "long", type: "conjunction" }).format(names);
	} catch {
		return names.join(", ");
	}
}

/**
 * The oldest aside suggestion waiting for the person driving the session:
 * who suggested it and its text on one line, with Send, Edit and Dismiss.
 * Only the driver sees it, so only the driver steers the agent.
 */
export function SuggestionBar({ suggestion, onSend, onEdit, onDismiss, busy, failed }: {
	suggestion: CloudCpAsideSuggestion;
	onSend: () => void;
	onEdit: () => void;
	onDismiss: () => void;
	busy?: boolean;
	failed?: boolean;
}) {
	const { t } = useTranslation();
	const name = suggestion.suggestedBy.name || t("chat.shared.someone");
	return (
		<div className="mb-2 flex min-w-0 flex-col" data-aside-suggestion={suggestion.suggestionId}>
			<div className={BAR_CLASS} role="region" aria-label={t("suggestion.suggests", { name })}>
				<PersonAvatar name={name} size="sm" />
				<p className="min-w-0 flex-1 truncate" title={suggestion.text}>
					<span className="text-muted-foreground">{t("suggestion.suggests", { name })}</span>
					<span className="text-foreground"> {suggestion.text}</span>
				</p>
				<Button disabled={busy} onClick={onSend} size="xs" type="button" variant="accent">{t("suggestion.send")}</Button>
				<Button disabled={busy} onClick={onEdit} size="xs" type="button" variant="secondary">{t("suggestion.edit")}</Button>
				<Button disabled={busy} onClick={onDismiss} size="xs" type="button" variant="quiet">{t("suggestion.dismiss")}</Button>
			</div>
			{failed ? <p className="px-3 pt-1 text-caption text-error" role="alert">{t("suggestion.failed")}</p> : null}
		</div>
	);
}

/** Before a send that mentions people who can't open the session: share it with them, or send without them. */
export function MentionAccessBar({ people, onShare, onSkip, sharing, failed }: {
	people: readonly Extract<ComposerMention, { kind: "person" }>[];
	onShare: () => void;
	onSkip: () => void;
	sharing?: boolean;
	failed?: boolean;
}) {
	const { t, i18n } = useTranslation();
	return (
		<div className="mb-2 flex min-w-0 flex-col" data-mention-access="">
			<div className={BAR_CLASS} role="alert">
				<Lock aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
				<p className="min-w-0 flex-1 truncate text-foreground">
					{t("mention.noAccess.title", { names: joinNames(people.map((person) => person.name), i18n.language) })}
				</p>
				<Button disabled={sharing} onClick={onShare} size="xs" type="button" variant="accent">{t("mention.noAccess.share")}</Button>
				<Button disabled={sharing} onClick={onSkip} size="xs" type="button" variant="quiet">{t("mention.noAccess.skip")}</Button>
			</div>
			{failed ? <p className="px-3 pt-1 text-caption text-error">{t("mention.noAccess.shareFailed")}</p> : null}
		</div>
	);
}

/** Before a send that mentions a team of more than five people: how many it notifies. */
export function MentionTeamBar({ team, onNotify, onCancel }: {
	team: Extract<ComposerMention, { kind: "team" }>;
	onNotify: () => void;
	onCancel: () => void;
}) {
	const { t } = useTranslation();
	const count = team.memberCount ?? 0;
	const withoutAccess = team.membersWithoutAccess ?? 0;
	return (
		<div className={`mb-2 ${BAR_CLASS}`} data-mention-team={team.id} role="alert">
			<TeamTile name={team.name} size="sm" />
			<p className="min-w-0 flex-1 truncate">
				<span className="text-foreground">{t("mention.team.title", { count, team: team.name })}</span>
				{withoutAccess > 0 ? <span className="text-muted-foreground"> {t("mention.team.withoutAccess", { count: withoutAccess })}</span> : null}
			</p>
			<Button onClick={onNotify} size="xs" type="button" variant="accent">{t("mention.team.notify")}</Button>
			<Button onClick={onCancel} size="xs" type="button" variant="quiet">{t("mention.team.cancel")}</Button>
		</div>
	);
}
