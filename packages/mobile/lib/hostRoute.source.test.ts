import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("retained-stack host routes", () => {
	it("does not unpair push while switching between saved machines", () => {
		const manager = source("./PushManager.tsx");
		const store = source("./store.tsx");
		const disconnect = source("./disconnect.ts");
		expect(store).toContain("setConfig(null)");
		expect(manager).not.toContain("unpairFromServer");
		expect(disconnect).toContain("await unpairFromServer(host)");
	});

	it("qualifies shell and preview links before they leave Chat", () => {
		const chat = source("./chat/ChatSessionScreen.tsx");
		expect(chat).toMatch(/pathname: "\/shell\/\[handleId\]"[^\n]*hostId:/);
		expect(chat).toMatch(/pathname: "\/preview\/\[id\]"[^\n]*hostId:/);
	});

	it("does not mount a terminal or preview for an unqualified route", () => {
		const shell = source("../app/shell/[handleId].tsx");
		const preview = source("../app/preview/[id].tsx");
		expect(shell).toContain("hostRouteMatches(routeHostId, currentHostId)");
		expect(shell).toContain("return <TerminalSessionScreen />");
		expect(preview).toContain("hostRouteMatches(routeHostId, currentHostId)");
		expect(preview).toContain("previewForConfig(");
	});
});
