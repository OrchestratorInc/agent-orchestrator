import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const terminal = readFileSync(new URL("./TerminalSessionScreen.tsx", import.meta.url), "utf8");
const notifications = readFileSync(new URL("../../app/notifications.tsx", import.meta.url), "utf8");

describe("Local-only actions on the combined board", () => {
	it("keeps terminal handles and restores on the paired desktop", () => {
		expect(terminal).toContain("host?.sessions.find");
		expect(terminal).toContain("restoreOn(source, id)");
		expect(terminal).toContain("refreshSource({ kind: \"local\", id: hostId })");
	});
	it("uses Local notifications and source-qualified routes", () => {
		expect(notifications).toContain("localBoard.sessions.find");
		expect(notifications).toContain("restoreOn(localSource, sessionId)");
		expect(notifications).toContain("source: localSource.kind, sourceId: localSource.id");
	});
});
