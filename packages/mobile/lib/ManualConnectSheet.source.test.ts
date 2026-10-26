import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

it("does not send machine A's saved bearer when manually pairing machine B", () => {
	const sheet = readFileSync(new URL("./ManualConnectSheet.tsx", import.meta.url), "utf8");
	const config = readFileSync(new URL("./config.ts", import.meta.url), "utf8");
	// Connect sends cfg.password to the typed address. The new-machine form must
	// start blank rather than load the active machine's saved credentials.
	expect(config).toMatch(/export const DEFAULT_CONFIG:[\s\S]*?host: ""[\s\S]*?password: ""/);
	expect(sheet).toContain('password: editingHost?.token ?? ""');
	expect(sheet).toContain("const [cfg, setCfg] = useState<ServerConfig>(() => {");
	expect(sheet).not.toMatch(/\bloadConfig\b/);
});

// iOS heads the form sheet with its native bar: the title, Cancel on the left
// and Connect (or Save) on the right, wired to the same handler and disabled
// state the body button had. Android keeps its sheet's title and button.
describe("manual connect sheet header", () => {
	const sheet = readFileSync(new URL("./ManualConnectSheet.tsx", import.meta.url), "utf8");
	const route = readFileSync(new URL("../app/sheets/connect.tsx", import.meta.url), "utf8");
	const layout = readFileSync(new URL("../app/_layout.tsx", import.meta.url), "utf8");

	it("shows the iOS bar from the first frame", () => {
		expect(layout).toMatch(/const CONNECT_SHEET_OPTIONS = \{[^}]*headerShown: Platform\.OS === "ios",/s);
		expect(layout).toContain('title: "Connect manually",');
		expect(layout).toContain('title: "Name this machine",');
	});

	it("titles the bar from the link and cancels from it in every state", () => {
		expect(route).toContain('<Stack.Screen options={{ title: hostId === undefined ? "Connect manually" : "Edit connection" }} />');
		expect(route).toContain('<Stack.Toolbar placement="left">\n\t\t\t\t<Stack.Toolbar.Button onPress={() => backOr(router)}>Cancel</Stack.Toolbar.Button>');
		expect(route.match(/\{header\}/g)).toHaveLength(3);
	});

	it("confirms from the bar on iOS with the form's handler and disabled state", () => {
		expect(sheet).toContain("const canConnect = !!machineName.trim() && !!cfg.host.trim() && validPort;");
		expect(sheet).toContain('<Stack.Toolbar.Button accessibilityLabel={editing ? "Save changes" : "Connect"} disabled={!canConnect} style={{ fontWeight: "600" }} onPress={() => void connectRef.current()}>');
		expect(sheet).toContain('<Spinner accessibilityLabel={editing ? "Saving changes" : "Connecting"} />');
		expect(sheet).toContain("<SheetScreen subtitle={subtitle}>");
	});

	it("keeps Android's title and button in the sheet body", () => {
		expect(sheet).toMatch(/<SheetHeader\s+title=\{title\}/);
		expect(sheet).toMatch(/\{ios \? null : \(\s*<Button\s+title=\{editingHost \? "Save changes" : "Connect"\}/);
		expect(sheet).toContain("disabled={!canConnect}\n\t\t\t\t\tonPress={connect}");
	});
});
