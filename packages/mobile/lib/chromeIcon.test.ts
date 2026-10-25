import { describe, expect, it } from "vitest";
import { resolveChromeIcon } from "./chromeIcon";
import { symbols } from "./symbols";

describe("resolveChromeIcon", () => {
	it("draws a registry name as its symbol", () => {
		expect(resolveChromeIcon("qrScanner")).toEqual({ symbol: symbols.qrScanner });
	});

	it("passes a one-off pair through", () => {
		const pair = { ios: "tray", android: "inbox" } as const;
		expect(resolveChromeIcon(pair)).toEqual({ symbol: pair });
	});

	it("draws a chrome Lucide name as the system symbol for the same job", () => {
		expect(resolveChromeIcon("refresh-cw")).toEqual({ symbol: symbols.refresh });
		expect(resolveChromeIcon("x")).toEqual({ symbol: symbols.close });
	});

	it("keeps Lucide for a name with no system symbol", () => {
		expect(resolveChromeIcon("git-pull-request")).toEqual({ lucide: "git-pull-request" });
	});

	it("does not mistake an inherited property for a registry name", () => {
		expect(resolveChromeIcon("constructor" as never)).toEqual({ lucide: "constructor" });
	});
});
