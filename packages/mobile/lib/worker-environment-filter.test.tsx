import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ select: vi.fn() }));

vi.mock("react-native", () => ({
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	Text: "Text",
	View: "View",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5 },
}));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { WorkerEnvironmentFilter } from "./worker-environment-filter";
import { ALL_WORKER_ENVIRONMENTS } from "./worker-controls";

let renderer: ReactTestRenderer | undefined;
const environments = [{ id: "mac", name: "Mac" }, { id: "windows", name: "Windows" }];

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.select.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

function render(selectedEnvironmentId: string) {
	const onSelectEnvironment = vi.fn();
	act(() => { renderer = create(<WorkerEnvironmentFilter environments={environments} selectedEnvironmentId={selectedEnvironmentId} onSelectEnvironment={onSelectEnvironment} />); });
	return onSelectEnvironment;
}
const chips = (): ReactTestInstance[] => renderer!.root.findAllByType("Pressable" as never);

// The one place to choose a machine on iOS, as Android's filter chips are.
it("lists All machines and each machine, the chosen one selected and checked", () => {
	render("windows");
	expect(chips().map((chip) => [chip.props.accessibilityLabel, chip.props.accessibilityState.selected])).toEqual([
		["All machines", false],
		["Mac", false],
		["Windows", true],
	]);
	expect(chips()[2].findAllByType("AppSymbol" as never)).toHaveLength(1);
	expect(chips()[1].findAllByType("AppSymbol" as never)).toHaveLength(0);
	// Each chip reaches the 44pt touch target.
	expect(chips()[0].props.hitSlop).toMatchObject({ top: 6, bottom: 6 });
});

it("chooses a machine, and the chosen one again goes back to every machine", () => {
	const choose = render(ALL_WORKER_ENVIRONMENTS);
	act(() => chips()[1].props.onPress());
	expect(choose).toHaveBeenLastCalledWith("mac");
	expect(fixtures.select).toHaveBeenCalledTimes(1);

	const again = render("mac");
	act(() => chips()[1].props.onPress());
	expect(again).toHaveBeenLastCalledWith(ALL_WORKER_ENVIRONMENTS);

	// All machines while already on it is no change, and no tick.
	const same = render(ALL_WORKER_ENVIRONMENTS);
	fixtures.select.mockClear();
	act(() => chips()[0].props.onPress());
	expect(same).not.toHaveBeenCalled();
	expect(fixtures.select).not.toHaveBeenCalled();
});
