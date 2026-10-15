import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Suspense, useState } from "react";

const hapticSelect = vi.hoisted(() => vi.fn());
const native = vi.hoisted(() => {
	const state = { value: "", get: () => state.value, set: vi.fn((value: string) => { state.value = value; }) };
	return state;
});

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
		Spacer: host("Spacer"), TextField: host("TextField"), Toggle: host("Toggle"), useNativeState: () => native,
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const noop = () => null;
	return {
		accessibilityIdentifier: noop, accessibilityLabel: noop, Animation: { spring: noop }, animation: noop,
		buttonBorderShape: noop, buttonStyle: noop, controlSize: noop, frame: noop, labelStyle: noop,
		menuOrder: (order: string) => ({ name: "menuOrder", order }), opacity: noop, padding: noop, scaleEffect: noop, textFieldStyle: noop, tint: noop,
	};
});

import { WorkerDock } from "./worker-dock.ios";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	hapticSelect.mockReset();
	native.value = "";
	native.set.mockClear();
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
					projectFiltered={false} onSelectEnvironment={setSelected} environments={[{ id: "mac", name: "Mac" }, { id: "dev", name: "Dev" }]}
					selectedEnvironmentId="mac"
					projectOptions={[{ id: '["local","mac","test"]', label: "Test project" }]}
					selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={setSelected} />
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
				projectFiltered={false} onSelectEnvironment={selectEnvironment}
				environments={[{ id: "mac", name: "Studio Mac", appearance: { symbol: "laptop" } }, { id: "dev", name: "Dev", appearance: { symbol: "server" } }, { id: "cloud", name: "Cloud", appearance: { symbol: "cloud" } }]}
				selectedEnvironmentId="mac"
				projectOptions={[{ id: '["local","mac","app"]', label: "AO" }]}
				selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()}
			/>);
		});

		const menus = renderer!.root.findAll((node) => String(node.type) === "Menu");
		// Upward-opening native menus must keep the presented item order.
		for (const menu of menus) expect(menu.props.modifiers).toContainEqual({ name: "menuOrder", order: "fixed" });
		const environment = menus.find((node) => node.props.label === "Environment");
		expect(environment).toBeDefined();
		expect(menus.filter((node) => node.props.label === "Environment")).toHaveLength(1);
		expect(menus.some((node) => node.props.label === "Machine")).toBe(false);
		expect(environment!.findAll((node) => String(node.type) === "Toggle").map((node) => node.props.label))
			.toEqual(["All environments", "Studio Mac", "Dev", "Cloud"]);
		expect(environment!.findAll((node) => String(node.type) === "Toggle" && node.props.isOn).map((node) => node.props.label)).toEqual(["Studio Mac"]);
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

it("keeps newer native search input while React acknowledges an earlier prefix, and still clears search", () => {
	function Screen() {
		const [query, setQuery] = useState("");
		return <WorkerDock query={query} onQueryChange={setQuery} onSpawn={vi.fn()}
			searchOpen onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
			projectFiltered={false} onSelectEnvironment={vi.fn()} environments={[]} selectedEnvironmentId="all"
			projectOptions={[]} selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()} />;
	}
	act(() => { renderer = create(<Screen />); });
	native.value = "Mobile QA verified";
	const field = renderer!.root.findAll((node) => String(node.type) === "TextField")[0];
	act(() => field.props.onTextChange("Mob"));
	expect(native.value).toBe("Mobile QA verified");
	expect(native.set).not.toHaveBeenCalled();
	act(() => field.props.onTextChange("Mobile QA verified"));
	const clear = renderer!.root.findAll((node) => String(node.type) === "Button" && node.props.label === "Clear and close search")[0];
	act(() => clear.props.onPress());
	expect(native.value).toBe("");
});

it("keeps AB when A and AB callbacks both arrive before the parent acknowledges A", () => {
	const screen = (query: string) => <WorkerDock query={query} onQueryChange={vi.fn()} onSpawn={vi.fn()}
		searchOpen onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
		projectFiltered={false} onSelectEnvironment={vi.fn()} environments={[]} selectedEnvironmentId="all"
		projectOptions={[]} selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()} />;
	act(() => { renderer = create(screen("")); });
	const change = (value: string) => { native.value = value; act(() => renderer!.root.findAll(node => String(node.type) === "TextField")[0].props.onTextChange(value)); };
	change("A"); change("AB");
	act(() => renderer!.update(screen("A")));
	expect(native.value).toBe("AB"); expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(screen("AB")));
	expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(screen("")));
	expect(native.value).toBe("");
	act(() => renderer!.update(screen("A")));
	expect(native.value).toBe("A");
});

it("clears native text even when the parent has not acknowledged any typing yet", () => {
	act(() => { renderer = create(<WorkerDock query="" onQueryChange={vi.fn()} onSpawn={vi.fn()}
		searchOpen onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
		projectFiltered={false} onSelectEnvironment={vi.fn()} environments={[]} selectedEnvironmentId="all"
		projectOptions={[]} selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()} />); });
	native.value = "AB";
	act(() => renderer!.root.findAll(node => String(node.type) === "TextField")[0].props.onTextChange("AB"));
	act(() => renderer!.root.findAll(node => String(node.type) === "Button" && node.props.label === "Clear and close search")[0].props.onPress());
	expect(native.value).toBe("");
});

it("coalesces repeated native values without mistaking a later external reset for an acknowledgement", () => {
	let resetQuery!: (query: string) => void;
	function Screen() {
		const [query, setQuery] = useState("");
		resetQuery = setQuery;
		return <WorkerDock query={query} onQueryChange={setQuery} onSpawn={vi.fn()}
			searchOpen onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
			projectFiltered={false} onSelectEnvironment={vi.fn()} environments={[]} selectedEnvironmentId="all"
			projectOptions={[]} selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()} />;
	}
	act(() => { renderer = create(<Screen />); });
	act(() => {
		for (const value of ["A", "AB", "A"]) {
			native.value = value;
			renderer!.root.findAll(node => String(node.type) === "TextField")[0].props.onTextChange(value);
		}
	});
	expect(native.value).toBe("A");
	expect(native.set).not.toHaveBeenCalled();
	act(() => resetQuery("AB"));
	expect(native.value).toBe("AB");
});

it("forwards new typing after Clear cancels the uncommitted parent echo and search reopens", () => {
	let reopen!: () => void;
	let parentQuery = "";
	const changes = vi.fn();
	function Screen() {
		const [query, setQuery] = useState("");
		parentQuery = query;
		const [open, setOpen] = useState(true);
		reopen = () => setOpen(true);
		return <WorkerDock query={query} onQueryChange={(next) => { changes(next); setQuery(next); }} onSpawn={vi.fn()}
			searchOpen={open} onSearchOpen={reopen} onSearchClose={() => { setQuery(""); setOpen(false); }} onOpenControls={vi.fn()}
			projectFiltered={false} onSelectEnvironment={vi.fn()} environments={[]} selectedEnvironmentId="all"
			projectOptions={[]} selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={vi.fn()} />;
	}
	act(() => { renderer = create(<Screen />); });
	act(() => {
		native.value = "AB";
		renderer!.root.findAll(node => String(node.type) === "TextField")[0].props.onTextChange("AB");
		renderer!.root.findAll(node => String(node.type) === "Button" && node.props.label === "Clear and close search")[0].props.onPress();
	});
	expect(native.value).toBe("");
	act(() => reopen());
	act(() => {
		native.value = "C";
		renderer!.root.findAll(node => String(node.type) === "TextField")[0].props.onTextChange("C");
	});
	expect(changes.mock.calls.map(call => call[0])).toEqual(["AB", "", "C"]);
	expect(parentQuery).toBe("C");
});

describe("iOS worker filter menu", () => {
	it("selects Cloud from the Environment submenu and keeps source-qualified projects", async () => {
		const selectEnvironment = vi.fn();
		const selectProject = vi.fn();
		await act(async () => {
			renderer = create(<WorkerDock
				query="" onQueryChange={vi.fn()} onSpawn={vi.fn()}
				searchOpen={false} onSearchOpen={vi.fn()} onSearchClose={vi.fn()} onOpenControls={vi.fn()}
				projectFiltered={false}
				environments={[{ id: "mac", name: "Studio Mac", appearance: { symbol: "laptop" } }, { id: "cloud", name: "Cloud", appearance: { symbol: "cloud" } }]}
				selectedEnvironmentId="cloud" onSelectEnvironment={selectEnvironment}
				projectOptions={[{ id: "all", label: "All projects" }, { id: "cloud:org-1:tap", label: "Tap · Cloud" }]}
				selectedProjectId="all" selectedProjectLabel="All projects" onSelectProject={selectProject}
			/>);
		});
		const menus = renderer!.root.findAll((node) => String(node.type) === "Menu");
		const environment = menus.find((node) => node.props.label === "Environment");
		expect(environment).toBeDefined();
		expect(environment!.findAll((node) => String(node.type) === "Toggle").map((node) => node.props.label))
			.toEqual(["All environments", "Studio Mac", "Cloud"]);
		const cloud = environment!.findAll((node) => String(node.type) === "Toggle" && node.props.label === "Cloud")[0];
		expect(cloud.props.systemImage).toBe("cloud");
		expect(environment!.findAll((node) => String(node.type) === "Toggle" && node.props.isOn).map((node) => node.props.label)).toEqual(["Cloud"]);
		act(() => cloud.props.onIsOnChange(true));
		expect(selectEnvironment).toHaveBeenCalledWith("cloud");
		expect(hapticSelect).toHaveBeenCalledOnce();
		const project = menus.find((node) => node.props.label === "All projects");
		expect(project!.findAll((node) => String(node.type) === "Button").map((node) => node.props.label))
			.toEqual(["All projects", "Tap · Cloud"]);
		const tap = project!.findAll((node) => String(node.type) === "Button" && node.props.label === "Tap · Cloud")[0];
		act(() => tap.props.onPress());
		expect(selectProject).toHaveBeenCalledWith("cloud:org-1:tap");
	});
});
