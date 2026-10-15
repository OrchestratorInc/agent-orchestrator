import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("react-native", () => ({ View: "View", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	const Picker = Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Picker", props, children), { Item: "PickerItem" });
	return { Host: "Host", Button: "Button", Picker };
});

import { SpawnComposerControls } from "./spawn-composer-controls";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("universal spawn destination picker", () => {
	it("shows machine and Cloud labels and keeps unavailable choices guarded", async () => {
		const onSelectDestination = vi.fn();
		await act(async () => {
			renderer = create(<SpawnComposerControls
				destinations={[
					{ source: { kind: "local", id: "host-a" }, label: "Alpha", available: true },
					{ source: { kind: "local", id: "host-b" }, label: "Beta", available: false },
					{ source: { kind: "cloud", id: "org-1" }, label: "Cloud", available: true },
				]}
				destination={{ kind: "local", id: "host-a" }}
				onSelectDestination={onSelectDestination}
				projects={[]} projectId={null} onSelectProject={vi.fn()}
				agents={[]} harness="" onSelectHarness={vi.fn()}
				models={[]} modelSelection="__auto__" modelLabel="Automatic" onSelectModel={vi.fn()}
				onAttach={vi.fn()} voice={{ state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }}
				onSpawn={vi.fn()} busy={false} disabled
			/>);
		});
		const picker = renderer!.root.findAll((node) => String(node.type) === "Picker")[0];
		expect(picker.findAll((node) => String(node.type) === "PickerItem").map((node) => [node.props.label, node.props.value])).toEqual([
			["Choose destination", ""],
			["Alpha", '["local","host-a"]'],
			["Beta · Unavailable", '["local","host-b"]'],
			["Cloud", '["cloud","org-1"]'],
		]);
		expect(picker.props.selectedValue).toBe('["local","host-a"]');
		await act(async () => picker.props.onValueChange('["local","host-b"]'));
		expect(onSelectDestination).not.toHaveBeenCalled();
		await act(async () => picker.props.onValueChange('["cloud","org-1"]'));
		expect(onSelectDestination).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
	});
});
