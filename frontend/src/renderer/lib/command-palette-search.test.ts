import { describe, expect, it } from "vitest";
import {
	buildChatSearchCommands,
	buildSessionSearchCommands,
	withoutChatHitsCoveredBySessionSearch,
	type SessionSearchCommandHit,
} from "./command-palette";

const hit: SessionSearchCommandHit = {
	sessionId: "proj-1",
	projectId: "proj",
	messageId: "msg-1",
	kind: "user",
	snippet: "the flaky login test",
	highlights: [{ start: 4, end: 9 }],
	sessionName: "Fix the login test",
	projectName: "proj",
	harness: "claude-code",
	sessionState: "terminated",
	createdAt: "2026-08-09T12:00:00Z",
};

describe("buildSessionSearchCommands", () => {
	it("labels a reviewer-chat match and carries its review to the open action", () => {
		const [command] = buildSessionSearchCommands([{ ...hit, reviewId: "review-1" }], "flaky");
		expect(command?.subtitle).toBe("Reviewer · proj · claude-code");
		expect(command?.action).toMatchObject({ kind: "open-search-result", sessionId: "proj-1", messageId: "msg-1", reviewId: "review-1" });
		const [plain] = buildSessionSearchCommands([hit], "flaky");
		expect(plain?.action).not.toHaveProperty("reviewId");
	});

	it("says how many of the session's messages matched", () => {
		const [command] = buildSessionSearchCommands([{ ...hit, sessionMatches: 7 }], "flaky");
		expect(command?.subtitle).toBe("you · 7 matches · proj · claude-code");
		const [single] = buildSessionSearchCommands([{ ...hit, sessionMatches: 1 }], "flaky");
		expect(single?.subtitle).toBe("you · 1 match · proj · claude-code");
		// A row found only by its name or metadata has no matched message to count.
		const [none] = buildSessionSearchCommands([{ ...hit, sessionMatches: 0 }], "flaky");
		expect(none?.subtitle).toBe("you · proj · claude-code");
	});

	it("opens a message match at its message, carrying the query for stepping", () => {
		const [command] = buildSessionSearchCommands([hit], "flaky");
		expect(command?.group).toBe("search");
		expect(command?.title).toBe("Fix the login test");
		expect(command?.subtitle).toBe("you · proj · claude-code");
		expect(command?.action).toEqual({
			kind: "open-search-result",
			projectId: "proj",
			sessionId: "proj-1",
			messageId: "msg-1",
			snippet: "the flaky login test",
			query: "flaky",
			// The matched span, so the opened message highlights the matched word.
			highlights: [{ start: 4, end: 9 }],
		});
	});

	it("opens a session-only match without inventing a message anchor", () => {
		const [command] = buildSessionSearchCommands(
			[{ ...hit, messageId: undefined, kind: "command", snippet: "TS2304: cannot find name" }],
			"TS2304",
		);
		expect(command?.subtitle).toBe("command · proj · claude-code");
		expect(command?.action).toMatchObject({ kind: "open-search-result", messageId: "" });
	});

	it("keeps the matched text in the row so the palette marks it", () => {
		const [command] = buildSessionSearchCommands([hit], "flaky");
		expect(command?.snippet).toEqual({ text: "the flaky login test", highlights: [{ start: 4, end: 9 }] });
		// The palette's snippet line walks the spans in order.
		const [unordered] = buildSessionSearchCommands(
			[{ ...hit, highlights: [{ start: 10, end: 15 }, { start: 4, end: 9 }] }],
			"flaky login",
		);
		expect(unordered?.snippet?.highlights).toEqual([{ start: 4, end: 9 }, { start: 10, end: 15 }]);
		// A recent session with nothing matched has no snippet line.
		const [recent] = buildSessionSearchCommands([{ ...hit, snippet: "", highlights: undefined }], "");
		expect(recent?.snippet).toBeUndefined();
	});

	it("names the machine a remote session is on, opens it there, and offers no local resume command", () => {
		const [row] = buildSessionSearchCommands([{ ...hit, hostId: "h_azure", machineLabel: "AzureLinux" }], "flaky");
		expect(row?.subtitle?.startsWith("AzureLinux · ")).toBe(true);
		expect(row?.action).toMatchObject({ kind: "open-search-result", hostId: "h_azure", sessionId: "proj-1" });
		// The resume command talks to this machine's daemon.
		expect(row?.secondaryAction).toBeUndefined();
		const [local] = buildSessionSearchCommands([hit], "flaky");
		expect(local?.id).not.toBe(row?.id);
		expect(local?.action).not.toHaveProperty("hostId");
	});

	it("offers the resume command as the row's ⌘↵ action", () => {
		const [command] = buildSessionSearchCommands([hit], "flaky");
		expect(command?.secondaryAction).toEqual({ kind: "copy-resume-command", command: "ao session resume-agent proj-1" });
	});

	it("gives every row a stable, distinct id even for repeated session matches", () => {
		const commands = buildSessionSearchCommands([hit, { ...hit }, { ...hit, messageId: "msg-2" }], "flaky");
		const ids = commands.map((command) => command.id);
		expect(new Set(ids).size).toBe(ids.length);
	});

	it("labels each kind so a row says why it matched", () => {
		const kinds = ["title", "user", "assistant", "tool", "command", "path"];
		const commands = buildSessionSearchCommands(
			kinds.map((kind, index) => ({ ...hit, kind, messageId: `msg-${index}` })),
			"flaky",
		);
		expect(commands.map((command) => command.subtitle?.split(" · ")[0])).toEqual([
			"session",
			"you",
			"agent",
			"tool",
			"command",
			"file",
		]);
	});
});
describe("withoutChatHitsCoveredBySessionSearch", () => {
	const chatHit = (sessionId: string, messageId: string, reviewId?: string) => ({
		messageId,
		sessionId,
		projectId: "proj",
		...(reviewId ? { reviewId } : {}),
		title: "Chat",
		text: "the [flaky] login test",
		createdAt: "2026-08-09T12:00:00Z",
	});

	it("drops chat rows for sessions the session-text group already lists", () => {
		const sessionRows = buildSessionSearchCommands([hit], "flaky");
		const chatRows = buildChatSearchCommands([chatHit("proj-1", "m1"), chatHit("proj-2", "m2")] as never);
		const kept = withoutChatHitsCoveredBySessionSearch(chatRows, sessionRows);
		expect(kept.map((item) => item.id)).toEqual(["message:m2"]);
	});

	it("keeps one row per session, reviewer chats included", () => {
		// The session-text row counts the reviewer chat's matches too, and the
		// opened session's find bar steps through them; a second row would repeat it.
		const sessionRows = buildSessionSearchCommands([hit], "flaky");
		const chatRows = buildChatSearchCommands([chatHit("proj-1", "m1", "review-1"), chatHit("proj-1", "m2"), chatHit("proj-9", "m3")] as never);
		expect(withoutChatHitsCoveredBySessionSearch(chatRows, sessionRows).map((item) => item.id)).toEqual(["message:m3"]);
	});

	it("does not let a session on another machine hide this machine's chat row", () => {
		// Session ids repeat across machines: proj-1 on AzureLinux is not proj-1 here.
		const remoteRows = buildSessionSearchCommands([{ ...hit, hostId: "h_azure", machineLabel: "AzureLinux" }], "flaky");
		const chatRows = buildChatSearchCommands([chatHit("proj-1", "m1")] as never);
		expect(withoutChatHitsCoveredBySessionSearch(chatRows, remoteRows)).toHaveLength(1);
	});

	it("keeps every chat row when there are no session-text rows", () => {
		const plain = buildChatSearchCommands([chatHit("proj-1", "m1"), chatHit("proj-1", "m2", "review-1")] as never);
		expect(withoutChatHitsCoveredBySessionSearch(plain, [])).toHaveLength(2);
	});
});
