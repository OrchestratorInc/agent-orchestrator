import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const select = vi.hoisted(() => vi.fn());

vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_, key) => `#${String(key)}` }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./haptics", () => ({ haptics: { select } }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		LazyRow: host("LazyRow"),
		FilterChip: Object.assign(host("FilterChip"), { LeadingIcon: host("LeadingIconSlot"), TrailingIcon: host("TrailingIconSlot"), Label: host("LabelSlot") }),
		Box: host("ComposeBox"),
		Icon: host("Icon"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	testID: (tag: string) => ({ testID: tag }),
	semantics: (config: object) => ({ semantics: config }),
	size: (width: number, height: number) => ({ size: [width, height] }),
	clip: (shape: unknown) => ({ clip: shape }),
	background: (color: string) => ({ background: color }),
	Shapes: { Circle: "circle" },
}));

import { WorkerEnvironmentFilter } from "./worker-environment-filter.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	select.mockClear();
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

const environments = [{ id: "mac", name: "Mac" }, { id: "dev", name: "Dev box" }];

async function render(selectedEnvironmentId: string, onSelectEnvironment = vi.fn()) {
	await act(async () => {
		renderer = create(<WorkerEnvironmentFilter environments={environments} selectedEnvironmentId={selectedEnvironmentId} onSelectEnvironment={onSelectEnvironment} />);
	});
	return onSelectEnvironment;
}
function chips(): ReactTestInstance[] {
	return renderer!.root.findAllByType("FilterChip" as never);
}
function label(chip: ReactTestInstance): string {
	return chip.findByType("LabelSlot" as never).findByType("ComposeText" as never).props.children;
}

describe("WorkerEnvironmentFilter on Android", () => {
	it("lists every environment, then each machine, and selects only the chosen one", async () => {
		await render("mac");
		expect(chips().map(label)).toEqual(["All machines", "Mac", "Dev box"]);
		expect(chips().map((chip) => chip.props.selected)).toEqual([false, true, false]);
	});

	it("selects a machine, and goes back to every environment from the chosen one", async () => {
		const onSelect = await render("mac");
		act(() => chips()[2].props.onClick());
		expect(onSelect).toHaveBeenLastCalledWith("dev");
		act(() => chips()[1].props.onClick());
		expect(onSelect).toHaveBeenLastCalledWith("mac");
		act(() => renderer!.update(<WorkerEnvironmentFilter environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={onSelect} />));
		act(() => chips()[1].props.onClick());
		expect(onSelect).toHaveBeenLastCalledWith("all");
		expect(select).toHaveBeenCalledTimes(3);
	});

	// The board behind the chips is rebuilt for the new machine, which takes a
	// while on a phone; the tap must not wait on it to show where it went.
	it("checks the tapped machine at once, and follows the board once it has moved", async () => {
		const onSelect = vi.fn();
		await render("all", onSelect);
		act(() => chips()[2].props.onClick());
		expect(onSelect).toHaveBeenCalledWith("dev");
		expect(chips().map((chip) => chip.props.selected)).toEqual([false, false, true]);
		expect(chips()[2].findAllByType("LeadingIconSlot" as never)).toHaveLength(1);
		// The board settles somewhere else (that machine went away meanwhile).
		act(() => renderer!.update(<WorkerEnvironmentFilter environments={environments} selectedEnvironmentId="mac" onSelectEnvironment={onSelect} />));
		expect(chips().map((chip) => chip.props.selected)).toEqual([false, true, false]);
	});

	it("does nothing when every environment is chosen again", async () => {
		const onSelect = await render("all");
		act(() => chips()[0].props.onClick());
		expect(onSelect).not.toHaveBeenCalled();
		expect(select).not.toHaveBeenCalled();
	});
});

// The machine's state on its chip: the trailing slot, since Material keeps the
// leading one for the check.
it("puts a red or amber dot in the trailing slot of a machine that may not be current", async () => {
	await act(async () => {
		renderer = create(<WorkerEnvironmentFilter
			environments={[{ id: "mac", name: "Mac", freshness: "unreachable" }, { id: "pc", name: "PC", freshness: "lagging" }, { id: "vm", name: "VM", freshness: "live" }]}
			selectedEnvironmentId="mac"
			onSelectEnvironment={vi.fn()}
		/>);
	});
	const dot = (index: number) => chips()[index].findAllByType("TrailingIconSlot" as never)[0]?.findByType("ComposeBox" as never).props.modifiers;
	expect(dot(1)).toEqual([{ size: [7, 7] }, { clip: "circle" }, { background: "#red" }, { testID: "worker-environment-freshness-mac" }]);
	expect(dot(2)).toContainEqual({ background: "#amber" });
	expect(chips()[3].findAllByType("TrailingIconSlot" as never)).toHaveLength(0);
	// TalkBack hears the state with the name; a live chip keeps its label alone.
	expect(chips()[1].props.modifiers).toContainEqual({ semantics: { contentDescription: "Mac, can't reach" } });
	expect(chips()[2].props.modifiers).toContainEqual({ semantics: { contentDescription: "PC, updates delayed" } });
	expect(chips()[3].props.modifiers).toEqual([{ testID: "worker-environment-chip-vm" }]);
	// The chosen chip keeps its check in the leading slot alongside the dot.
	expect(chips()[1].findAllByType("LeadingIconSlot" as never)).toHaveLength(1);
});
