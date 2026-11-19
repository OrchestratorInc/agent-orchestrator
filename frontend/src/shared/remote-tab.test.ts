import { describe, expect, it } from "vitest";
import { REMOTE_TAB_REPORT, parseRemoteTabReport } from "./remote-tab";

const PNG_ICON = "data:image/png;base64,iVBORw0KGgo=";

describe("parseRemoteTabReport", () => {
	it("reads a viewer's report of the agent browser page", () => {
		const report = parseRemoteTabReport(REMOTE_TAB_REPORT + JSON.stringify({
			tabId: "R1", url: "https://a.example/x", title: "A", favicon: PNG_ICON, loading: true, back: true, fwd: false,
			follow: "R2", closed: { next: "R0" }, driving: true, tabs: [{ id: "R1", title: "A", url: "https://a.example/x" }, { title: "no id" }],
		}));
		expect(report).toEqual({
			tabId: "R1", url: "https://a.example/x", title: "A", favicon: PNG_ICON, loading: true, back: true, fwd: false,
			follow: "R2", closed: { next: "R0" }, shown: false, tabs: [{ id: "R1", title: "A", url: "https://a.example/x" }],
		});
	});

	it("keeps each listed tab's title and address, capped and filtered as the tab's own", () => {
		const tabs = (listed: unknown[]) => parseRemoteTabReport(REMOTE_TAB_REPORT + JSON.stringify({ tabId: "R1", tabs: listed }))?.tabs;
		expect(tabs([
			{ id: "R1".padEnd(80, "x"), title: "T".repeat(2000), url: `https://a.example/${"p".repeat(9000)}` },
			{ id: "R2", title: 7, url: "javascript:alert(1)" },
			{ id: "R3", url: "file:///etc/passwd" },
			null,
			"R4",
		])).toEqual([
			{ id: "R1".padEnd(64, "x"), title: "T".repeat(1024), url: `https://a.example/${"p".repeat(9000)}`.slice(0, 8192) },
			{ id: "R2", title: "", url: "" },
			{ id: "R3", title: "", url: "" },
		]);
		expect(tabs(Array.from({ length: 150 }, (_, index) => ({ id: `R${index}` })))).toHaveLength(100);
	});

	it("takes the tab icon only as an inline image, never as an address to fetch", () => {
		const icon = (favicon: unknown) =>
			parseRemoteTabReport(REMOTE_TAB_REPORT + JSON.stringify({ tabId: "R1", favicon }))?.favicon;
		for (const type of ["png", "x-icon", "vnd.microsoft.icon", "gif", "jpeg", "webp", "svg+xml"]) {
			expect(icon(`data:image/${type};base64,AAAA`)).toBe(`data:image/${type};base64,AAAA`);
		}
		expect(icon("data:image/png;base64,AAA=")).toBe("data:image/png;base64,AAA=");
		expect(icon("data:image/png;base64,AA==")).toBe("data:image/png;base64,AA==");
		// The longest that fits the 90,000-character cap (22 + 89,976).
		expect(icon(`data:image/png;base64,${"A".repeat(89_976)}`)).toHaveLength(89_998);
		for (const dropped of [
			"https://a.example/icon.png",
			"http://192.168.1.1/admin/reboot",
			"//a.example/icon.png",
			"data:image/png;base64,",
			"data:image/png;base64,x",
			"data:image/png;base64,AAAA AAAA",
			"data:image/png,AAAA",
			"data:image/bmp;base64,AAAA",
			"data:text/html;base64,AAAA",
			"DATA:image/png;base64,AAAA",
			`data:image/png;base64,${"A".repeat(89_980)}`,
			42,
		]) {
			expect(icon(dropped)).toBe("");
		}
	});

	it("rejects anything else, and lets only web addresses reach the address bar", () => {
		expect(parseRemoteTabReport("hello")).toBeNull();
		expect(parseRemoteTabReport(REMOTE_TAB_REPORT + "{not json")).toBeNull();
		expect(parseRemoteTabReport(REMOTE_TAB_REPORT + JSON.stringify({ url: "https://a.example/" }))).toBeNull();
		const report = parseRemoteTabReport(REMOTE_TAB_REPORT + JSON.stringify({ tabId: "R1", url: "javascript:alert(1)", favicon: "data:image/png;base64,x" }));
		expect(report).toMatchObject({ url: "", favicon: "", tabs: [] });
	});
});
