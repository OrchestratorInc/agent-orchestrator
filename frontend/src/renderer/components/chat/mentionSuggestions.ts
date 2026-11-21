import { mentionSections } from "../../lib/cloud-mentions";
import type { CloudCpMentionPerson, CloudCpMentionTeam } from "../../lib/cloud-cp/types";
import type { Suggestion } from "./composerSuggest";

/** The @ menu's section titles and the "Here now" badge, in the reader's language. */
export interface MentionMenuLabels {
	inSession: string;
	teams: string;
	/** "People in {workspace}", or "People in this workspace" without its name. */
	workspace: string;
	hereNow: string;
}

/**
 * The @ menu's people and teams for a query, as menu rows in its fixed order:
 * the people in the session (those here now first), the workspace's teams,
 * then its other people. Picking one makes a mention chip. Only what the
 * control plane returned is offered; the signed-in person never is.
 */
export function mentionSuggestions(
	candidates: { people: readonly CloudCpMentionPerson[]; teams: readonly CloudCpMentionTeam[] } | undefined,
	query: string,
	selfUserId: string | undefined,
	labels: MentionMenuLabels,
): Suggestion[] {
	const sections = mentionSections(candidates, query, selfUserId);
	const person = (row: CloudCpMentionPerson, section: string): Suggestion => ({
		value: `person:${row.userId}`,
		label: row.name,
		section,
		badge: row.present ? labels.hereNow : undefined,
		mention: { kind: "person", id: row.userId, name: row.name },
	});
	return [
		...sections.inSession.map((row) => person(row, labels.inSession)),
		...sections.teams.map((team): Suggestion => ({
			value: `team:${team.teamId}`,
			label: team.name,
			section: labels.teams,
			mention: { kind: "team", id: team.teamId, name: team.name },
		})),
		...sections.workspace.map((row) => person(row, labels.workspace)),
	];
}
