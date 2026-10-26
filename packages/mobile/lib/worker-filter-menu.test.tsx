import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Suspense, useState } from "react";

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Stack: {
			Toolbar: Object.assign(host("StackToolbar"), {
				Badge: host("StackToolbarBadge"),
				Menu: host("StackToolbarMenu"),
				MenuAction: host("StackToolbarMenuAction"),
			}),
		},
	};
});
vi.mock("react-native", () => ({ Platform: { OS: "ios" } }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn() } }));

import { workerFilterMenu } from "./worker-filter-menu";

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

// The native menu is still dismissing when a choice lands, and the board for
// the new scope may suspend while it loads: the old board stays up meanwhile.
it.each(["Dev", "Test project"])("keeps the existing board usable while choosing %s prepares its replacement", async (label) => {
	const pending = new Promise<void>(() => {});
	function Board({ selected }: { selected: string }) {
		if (selected !== "mac") throw pending;
		return <p>Mac list</p>;
	}
	function Screen() {
		const [selected, setSelected] = useState("mac");
		return <Suspense fallback={<p>Loading replacement</p>}>
			{workerFilterMenu({
				environments: [{ id: "mac", name: "Mac" }, { id: "dev", name: "Dev" }],
				selectedEnvironmentId: "mac",
				onSelectEnvironment: setSelected,
				projects: [{ id: "test", name: "Test project", hostId: "mac" }],
				selectedProjectId: "all",
				onSelectProject: setSelected,
				badgeColor: "#accent",
			})}
			<Board selected={selected} />
		</Suspense>;
	}
	await act(async () => { renderer = create(<Screen />); });
	const choice = renderer!.root.findAllByType("StackToolbarMenuAction" as never).find((node) => node.props.children === label)!;
	await act(async () => { choice.props.onPress(); });
	const visible = JSON.stringify(renderer!.toJSON(), (key, value) => key === "props" ? undefined : value);
	expect(visible).not.toContain("Loading replacement");
	expect(visible).toContain("Mac list");
});
