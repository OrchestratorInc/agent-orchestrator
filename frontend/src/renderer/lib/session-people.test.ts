import { describe, expect, it } from "vitest";
import type { CloudCpSessionLive } from "./cloud-cp";
import { orderSessionPeople, personInitials, sessionPeopleFromLive, someoneElseWorking, type SessionPerson } from "./session-people";

const person = (userId: string, state: SessionPerson["state"], present = false): SessionPerson => ({ userId, name: userId, present, state });

describe("session people", () => {
	it("puts whoever is writing first, then whoever is in line, then whoever is here", () => {
		const ordered = orderSessionPeople([
			person("owner", "idle"),
			person("here", "idle", true),
			person("waiting", "waiting", true),
			person("writer", "working", true),
			person("away", "idle"),
		]);
		expect(ordered.map((p) => p.userId)).toEqual(["writer", "waiting", "here", "owner", "away"]);
	});

	it("reads who is here and what they are doing from the live state", () => {
		const live: CloudCpSessionLive = {
			type: "session.live",
			viewers: [
				{ userId: "a", name: "Alice", role: "member" },
				{ userId: "b", name: "Bob", avatarUrl: "https://lh3.googleusercontent.com/bob", role: "editor" },
				{ userId: "c", name: "Carol", role: "viewer" },
			],
			composer: { holder: { userId: "b", name: "Bob" }, epoch: 3, waitlist: [{ userId: "a", name: "Alice" }] },
			canTakeOver: true, you: "a", chatSettings: {}, canConfigure: true,
		};
		expect(sessionPeopleFromLive(live)).toEqual([
			{ userId: "a", name: "Alice", present: true, state: "waiting" },
			{ userId: "b", name: "Bob", avatarUrl: "https://lh3.googleusercontent.com/bob", present: true, state: "working" },
			{ userId: "c", name: "Carol", present: true, state: "idle" },
		]);
	});

	it("names someone else at work, never the person themselves", () => {
		const people = [person("me", "working", true), person("other", "idle", true)];
		expect(someoneElseWorking(people, "me")).toBeUndefined();
		expect(someoneElseWorking(people, "other")?.userId).toBe("me");
	});

	it("takes up to two initials", () => {
		expect(personInitials("Vaanya Gupta")).toBe("VG");
		expect(personInitials("vaibhaav")).toBe("V");
		expect(personInitials("  ")).toBe("?");
	});
});
