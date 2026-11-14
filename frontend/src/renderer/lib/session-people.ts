// The people in a shared cloud session, as the board card and the session
// header show them: whoever is writing to the agent first, then whoever is in
// line, then whoever has it open, then everyone else.

import type { CloudCpSessionLive, CloudCpSessionParticipant } from "./cloud-cp";

export type SessionPerson = Pick<CloudCpSessionParticipant, "userId" | "name" | "avatarUrl" | "present" | "state">;

/** At most this many circles show; the rest fold into "+N". */
export const SESSION_PEOPLE_SHOWN = 2;

const STATE_ORDER: Record<SessionPerson["state"], number> = { working: 0, waiting: 1, idle: 2 };

/** People in the order the circles show them. Stable within each group. */
export function orderSessionPeople<T extends SessionPerson>(people: readonly T[]): T[] {
	return people
		.map((person, index) => ({ person, index }))
		.sort((left, right) =>
			STATE_ORDER[left.person.state] - STATE_ORDER[right.person.state] ||
			Number(right.person.present) - Number(left.person.present) ||
			left.index - right.index)
		.map(({ person }) => person);
}

/** Everyone with the session open, from its live state, with what they are doing. */
export function sessionPeopleFromLive(live: CloudCpSessionLive): SessionPerson[] {
	const waiting = new Set(live.composer.waitlist.map((waiter) => waiter.userId));
	return live.viewers.map((viewer) => ({
		userId: viewer.userId,
		name: viewer.name,
		...(viewer.avatarUrl ? { avatarUrl: viewer.avatarUrl } : {}),
		present: true,
		state: live.composer.holder?.userId === viewer.userId ? "working" : waiting.has(viewer.userId) ? "waiting" : "idle",
	}));
}

/** The person other than me writing to the agent now, if any. */
export function someoneElseWorking<T extends SessionPerson>(people: readonly T[], me: string | undefined): T | undefined {
	return people.find((person) => person.state === "working" && person.userId !== me);
}

/** Initials for a person's circle: up to two letters from their name. */
export function personInitials(name: string): string {
	const letters = name.trim().split(/[\s._-]+/).filter(Boolean).slice(0, 2).map((part) => part[0]?.toUpperCase() ?? "");
	return letters.join("") || "?";
}
