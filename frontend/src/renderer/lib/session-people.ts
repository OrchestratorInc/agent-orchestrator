import type { CloudCpSessionLive, CloudCpSessionParticipant } from "./cloud-cp";

export type SessionPerson = Pick<CloudCpSessionParticipant, "userId" | "name" | "avatarUrl" | "present" | "state">;

export const SESSION_PEOPLE_SHOWN = 2;

const STATE_ORDER: Record<SessionPerson["state"], number> = { working: 0, waiting: 1, idle: 2 };

export function orderSessionPeople<T extends SessionPerson>(people: readonly T[]): T[] {
	return people
		.map((person, index) => ({ person, index }))
		.sort((left, right) =>
			STATE_ORDER[left.person.state] - STATE_ORDER[right.person.state] ||
			Number(right.person.present) - Number(left.person.present) ||
			left.index - right.index)
		.map(({ person }) => person);
}

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

export function someoneElseWorking<T extends SessionPerson>(people: readonly T[], me: string | undefined): T | undefined {
	return people.find((person) => person.state === "working" && person.userId !== me);
}

export function personInitials(name: string): string {
	const letters = name.trim().split(/[\s._-]+/).filter(Boolean).slice(0, 2).map((part) => part[0]?.toUpperCase() ?? "");
	return letters.join("") || "?";
}
