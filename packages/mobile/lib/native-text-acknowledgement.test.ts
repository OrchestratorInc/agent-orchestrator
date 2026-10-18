import { execFileSync } from "node:child_process";
import { expect, it } from "vitest";

it.each(["field", "search"])("keeps native input when another %s callback arrives after commit and before passive effects", (kind) => {
	// The renderer's CommonJS scheduler must be replaced before React loads.
	// Run in a child process so the scheduler mock cannot affect other tests.
	const result = execFileSync(process.execPath, ["-e", String.raw`
const fs = require("node:fs");
const Module = require("node:module");
const path = require("node:path");
const ts = require("typescript");
const Scheduler = require("scheduler/unstable_mock");
const native = { value: "", writes: [], get() { return this.value; }, set(value) { this.writes.push(value); } };
const originalLoad = Module._load;
Module._load = function(name, parent, main) {
 if (name === "scheduler") return Scheduler;
 if (name === "@expo/ui") return { Host: "Host" };
 if (name === "@expo/ui/swift-ui") return new Proxy({ useNativeState: () => native }, { get: (target, key) => key in target ? target[key] : String(key) });
 if (name === "@expo/ui/swift-ui/modifiers") return new Proxy({}, { get: (_, key) => key === "Animation" ? { spring: () => ({}) } : () => ({}) });
 if (name.endsWith("/glass")) return { glassPanel: () => ({}), glassCircle: () => ({}), glassField: () => ({}) };
 if (name.endsWith("/haptics")) return { haptics: {} };
 if (name.endsWith("/ThemeProvider")) return { useTheme: () => ({}), useThemeState: () => ({ scheme: "light" }) };
 if (name.endsWith("/tokens")) return { iconSize: {}, type: {}, duration: {} };
 if (name === "./native-header-button.ios") return { GLASS_CIRCLE_SIZE: 52 };
 if (name === "./worker-controls") return { workerEnvironmentOptions: () => [], workerProjectOptions: () => [], workerProjectLabel: () => "All" };
 if (name === "./worker-dock-layout") return { workerDockVisibility: () => ({ showSearch: true }) };
 if (name === "./worker-search") return { workerSearchClearState: () => ({ disabled: false }) };
 if (name === "./machineAppearance" || name === "./machine-menu-image") return {};
 return originalLoad.call(this, name, parent, main);
};
// Load the real shared hook along with the real component.
Module._extensions[".ts"] = (loaded, filename) => loaded._compile(ts.transpileModule(fs.readFileSync(filename, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText, filename);
const React = require("react");
const Renderer = require("react-test-renderer");
const isSearch = process.argv[1] === "search";
const file = path.resolve(isSearch ? "lib/worker-dock.ios.tsx" : "lib/chat/elicitation-native-controls.ios.tsx");
const componentModule = new Module(file, module);
componentModule.filename = file;
componentModule.paths = Module._nodeModulePaths(path.dirname(file));
componentModule._compile(ts.transpileModule(fs.readFileSync(file, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText, file);
const Component = componentModule.exports[isSearch ? "WorkerDock" : "ElicitationTextField"];
function Tail({ value }) { return React.createElement("tail", { value }); }
function Harness() {
 const [value, setValue] = React.useState("");
 const props = isSearch ? { query: value, onQueryChange: setValue, searchOpen: true, environments: [], projects: [] } : { value, onChange: setValue, label: "Title" };
 return React.createElement(React.Fragment, null, React.createElement(Component, props), React.createElement(Tail, { value }));
}
const renderer = Renderer.create(React.createElement(Harness));
Scheduler.unstable_flushAllWithoutAsserting();
native.value = "Mobile QA verified!";
renderer.root.findByType("TextField").props.onTextChange("Mob");
Scheduler.unstable_flushUntilNextPaint();
const checkpoint = renderer.root.findByType("tail").props.value;
renderer.root.findByType("TextField").props.onTextChange("Mobile QA verified");
Scheduler.unstable_flushAllWithoutAsserting();
console.log(JSON.stringify({ checkpoint, committed: renderer.root.findByType("tail").props.value, writes: native.writes }));
renderer.unmount(); Scheduler.unstable_flushAllWithoutAsserting();
`, kind], { cwd: process.cwd(), encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
	expect(JSON.parse(result)).toEqual({ checkpoint: "Mob", committed: "Mobile QA verified", writes: [] });
});
