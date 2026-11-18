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
		FilterChip: Object.assign(host("FilterChip"), { LeadingIcon: host("LeadingIconSlot"), Label: host("LabelSlot") }),
		Icon: host("Icon"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ testID: (tag: string) => ({ testID: tag }) }));

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
		expect(onSelect).toHaveBeenLastCalledWith("all");
		expect(select).toHaveBeenCalledTimes(2);
	});

	it("does nothing when every environment is chosen again", async () => {
		const onSelect = await render("all");
		act(() => chips()[0].props.onClick());
		expect(onSelect).not.toHaveBeenCalled();
		expect(select).not.toHaveBeenCalled();
	});
});
