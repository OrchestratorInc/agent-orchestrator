import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

it("does not send machine A's saved bearer when manually pairing machine B", () => {
	const form = read("./useManualConnect.ts");
	const config = read("./config.ts");
	// Connect sends cfg.password to the typed address. The new-machine form must
	// start blank rather than load the active machine's saved credentials.
	expect(config).toMatch(/export const DEFAULT_CONFIG:[\s\S]*?host: ""[\s\S]*?password: ""/);
	expect(form).toContain('password: editingHost?.token ?? ""');
	expect(form).toContain("const [cfg, setCfg] = useState<ServerConfig>(() => {");
	expect(form).not.toMatch(/\bloadConfig\b/);
});

// iOS heads the form sheet with its native bar: the title, Cancel on the left
// and Connect (or Save) on the right. Android keeps its sheet's title and button.
describe("manual connect sheet header", () => {
	const form = read("./useManualConnect.ts");
	const route = read("../app/sheets/connect.tsx");
	const layout = read("../app/_layout.tsx");

	it("shows the iOS bar from the first frame", () => {
		expect(layout).toMatch(/const CONNECT_SHEET_OPTIONS = \{[^}]*headerShown: Platform\.OS === "ios",/s);
		expect(layout).toContain('title: "Connect manually",');
		expect(layout).toContain('title: "Name this machine",');
	});

	// The iOS body is a SwiftUI Form, which fills its sheet rather than
	// measuring to fit, so the sheet opens at a set height.
	it("gives the iOS form sheets a fixed height", () => {
		expect(layout).toMatch(/const CONNECT_SHEET_OPTIONS = \{[^}]*sheetAllowedDetents: Platform\.OS === "ios" \? \[1\] : \[0\.6, 0\.95\],/s);
	});

	it("titles the bar from the link and cancels from it in every state", () => {
		expect(route).toContain('<Stack.Screen options={{ title: hostId === undefined ? "Connect manually" : "Edit connection" }} />');
		expect(route).toContain('<Stack.Toolbar placement="left">\n\t\t\t\t<Stack.Toolbar.Button disabled={busy} onPress={() => backOr(router)}>Cancel</Stack.Toolbar.Button>');
		expect(route).toContain(") : null, [busy, hostId, router]);");
		expect(route.match(/\{header\}/g)).toHaveLength(3);
	});

	// A started connect can't be called back. Leaving mid-request used to let
	// onConnected close the sheet a second time, popping the pair flow under it.
	it("holds the sheet while connecting and closes it only while it is still on top", () => {
		expect(form).toContain("useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);");
		expect(route).toContain("onBusyChange={setBusy}");
		expect(route).toContain("if (!busy) return;\n\t\tnavigation.setOptions({ gestureEnabled: false });\n\t\treturn () => navigation.setOptions({ gestureEnabled: true });\n\t}, [busy, navigation]);");
		expect(route).toContain("if (!navigation.isFocused()) return;\n\t\t\t\t\tbackOr(router);\n\t\t\t\t\tdone?.();");
	});
});
