import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Suspense, useState } from "react";

const hapticSelect = vi.hoisted(() => vi.fn());

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "#ffffff", textSecondary: "#999999", textPrimary: "#ffffff" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./haptics", () => ({ haptics: { select: hapticSelect, tap: vi.fn() } }));
vi.mock("./glass", () => ({ glassCircle: () => null, glassField: () => null }));
vi.mock("./native-header-button.ios", () => ({ GLASS_CIRCLE_SIZE: 52 }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("Button"), GlassEffectContainer: host("GlassEffectContainer"), Group: host("Group"),
		HStack: host("HStack"), Image: host("Image"), Menu: host("Menu"), Section: host("Section"),
		Spacer: host("Spacer"), TextField: host("TextField"), Toggle: host("Toggle"), useNativeState: (value: string) => ({ get: () => value, set: vi.fn() }),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const noop = () => null;
	return {
		accessibilityIdentifier: noop, accessibilityLabel: noop, Animation: { spring: noop }, animation: noop,
		buttonBorderShape: noop, buttonStyle: noop, controlSize: noop, frame: noop, labelStyle: noop,
		menuOrder: noop, opacity: noop, padding: noop, scaleEffect: noop, textFieldStyle: noop, tint: noop,
	};
});

import { WorkerDock } from "./worker-dock.ios";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	hapticSelect.mockReset();
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

describe("iOS worker environment filter menu", () => {
	it.each(["environment", "project"])("keeps the existing board usable while a native %s menu selection prepares its replacement", async (kind) => {
		const pending = new Promise<void>(() => {});
		function Board({ selected }: { selected: string }) {
			if (selected !== "mac") throw pending;
			return <p>Mac list</p>;
		}
		function Screen() {
			const [selected, setSelected] = useState("mac");
			return <Suspense fallback={<p>Loading replacement</p>}>
				<WorkerDock query="" onQueryChange={vi.fn()} onSpawn={vi.fn()}
					searchOpen={false} onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
					projectFiltered={false} environments={[{ id: "mac", name: "Mac" }, { id: "dev", name: "Dev" }]}
					selectedEnvironmentId="mac" onSelectEnvironment={setSelected}
					projects={[{ id: "test", name: "Test project", hostId: "mac" }]}
					selectedProjectId="all" onSelectProject={setSelected} />
				<Board selected={selected} />
			</Suspense>;
		}
		await act(async () => { renderer = create(<Screen />); });
		const choice = renderer!.root.findAll(node => String(node.type) === (kind === "environment" ? "Toggle" : "Button") && node.props.label === (kind === "environment" ? "Dev" : "Test project"))[0];
		await act(async () => { if (kind === "environment") choice.props.onIsOnChange(true); else choice.props.onPress(); });
		const visible = JSON.stringify(renderer!.toJSON(), (key, value) => key === "props" ? undefined : value);
		expect(visible).not.toContain("Loading replacement");
		expect(visible).toContain("Mac list");
	});
	it("selects a named machine from the Environment submenu", async () => {
		const selectEnvironment = vi.fn();
		await act(async () => {
			renderer = create(<WorkerDock
				query="" onQueryChange={vi.fn()} onSpawn={vi.fn()}
				searchOpen={false} onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
				projectFiltered={false}
				environments={[{ id: "mac", name: "Studio Mac", appearance: { symbol: "laptop" } }, { id: "dev", name: "Dev", appearance: { symbol: "server" } }]}
				selectedEnvironmentId="mac" onSelectEnvironment={selectEnvironment}
				projects={[{ id: "app", name: "AO", hostId: "mac", hostName: "Studio Mac" }]}
				selectedProjectId="all" onSelectProject={vi.fn()}
			/>);
		});

		const menus = renderer!.root.findAll((node) => String(node.type) === "Menu");
		const environment = menus.find((node) => node.props.label === "Environment");
		expect(environment).toBeDefined();
		expect(environment!.props.systemImage).toBe("tray.2");
		expect(environment!.findAll((node) => String(node.type) === "Toggle").map((node) => node.props.label))
			.toEqual(["All environments", "Studio Mac", "Dev"]);
		const mac = environment!.findAll((node) => String(node.type) === "Toggle" && node.props.label === "Studio Mac")[0];
		expect(mac.props.systemImage).toBe("laptopcomputer");
		expect(mac.props.isOn).toBe(true);
		const dev = environment!.findAll((node) => String(node.type) === "Toggle" && node.props.label === "Dev")[0];
		expect(dev.props.systemImage).toBe("server.rack");
		act(() => dev.props.onIsOnChange(true));
		expect(selectEnvironment).toHaveBeenCalledWith("dev");
		expect(hapticSelect).toHaveBeenCalledOnce();
	});
});
