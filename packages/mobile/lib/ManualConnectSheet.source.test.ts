import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

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

// A started connect can't be called back. Leaving mid-request used to let
// onConnected close the form a second time, popping the pair flow under it.
it("holds the page while connecting and closes it only while it is still on top", () => {
	const form = read("./useManualConnect.ts");
	const route = read("../app/connect.tsx");
	expect(form).toContain("useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);");
	expect(route).toContain("onBusyChange={setBusy}");
	expect(route).toContain("if (!busy) return;\n\t\tnavigation.setOptions({ gestureEnabled: false });\n\t\treturn () => navigation.setOptions({ gestureEnabled: true });\n\t}, [busy, navigation]);");
	expect(route).toContain("if (!navigation.isFocused()) return;\n\t\t\t\t\tbackOr(router);\n\t\t\t\t\tdone?.();");
});
