import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HostedProjectRow } from "./hostedRows";
import { MachineIdentity } from "./MachineIdentity";
import type { OrchestratorProjectRow } from "./orchestratorView";

const fixture = vi.hoisted(() => ({ scheme: "dark" as "dark" | "light" }));

vi.mock("./ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("./theme");
	const current = () => fixture.scheme === "dark" ? darkTheme : lightTheme;
	return { useTheme: current, useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(current()) };
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./ui", async () => {
	const React = await import("react");
	return { Dot: (props: object) => React.createElement("Dot", props) };
});
vi.mock("./orchestrator-icon", async () => {
	const React = await import("react");
	return { OrchestratorIcon: (props: object) => React.createElement("OrchestratorIcon", props) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), Pressable: host("Pressable"),
		Platform: { OS: "ios" },
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"), View: host("View"),
	};
});

import { ProjectCard, ProjectMachineIndicator } from "./project-card";

const row: OrchestratorProjectRow = {
	project: { id: "project-1", name: "Tap", kind: "single_repo" },
	link: null,
	workers: [],
	section: "not-running",
	action: "start",
	detail: "",
	activityAt: null,
	urgency: 0,
};

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.scheme = "dark";
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

describe("project source identity", () => {
	it.each([
		"dark",
		"light",
	] as const)("identifies Cloud projects once with main's name and symbol treatment in %s mode", async (scheme) => {
		fixture.scheme = scheme;
		await act(async () => { renderer = create(<ProjectCard row={row} sourceLabel="Cloud" busy={false} onOpenProject={vi.fn()} />); });
		expect(renderer!.root.findAll((node) => String(node.type) === "Feather" && node.props.name === "cloud")).toHaveLength(1);
		expect(renderer!.root.findAllByType(MachineIdentity)).toHaveLength(1);
		expect(renderer!.root.findByType(MachineIdentity).props.appearance).toEqual({ symbol: "cloud" });
		expect(renderer!.root.findAll((node) => String(node.type) === "Text" && node.props.children === "Cloud")).toHaveLength(1);
		const rowButton = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityRole === "button")[0];
		expect(rowButton.props.accessibilityLabel).toContain("Cloud");
	});

	it.each(["dark", "light"] as const)("shows one quiet Cloud identity in the project header in %s mode", async (scheme) => {
		fixture.scheme = scheme;
		await act(async () => {
			renderer = create(<ProjectMachineIndicator source={{ kind: "cloud", id: "org-1" }} hosts={[
				{ hostId: "org-1", name: "Build Laptop", appearance: { symbol: "laptop" } },
			]} />);
		});
		expect(renderer!.root.findAllByType(MachineIdentity)).toHaveLength(1);
		const identity = renderer!.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Cloud");
		expect(identity.props.appearance).toEqual({ symbol: "cloud" });
		const name = renderer!.root.find((node) => String(node.type) === "Text" && node.props.children === "Cloud");
		expect(name.props.numberOfLines).toBe(1);
		expect(name.props.ellipsizeMode).toBe("tail");
		const symbol = renderer!.root.find((node) => String(node.type) === "Feather" && node.props.name === "cloud");
		expect(symbol.props.color).toBe(name.props.style.color);
		const view = renderer!.root.find((node) => String(node.type) === "View" && node.props.testID === "project-machine-identity");
		const style = Object.assign({}, ...view.props.style);
		expect(style).toMatchObject({ minWidth: 0, maxWidth: 132, flexShrink: 1 });
		expect(style.borderWidth).toBeUndefined();
		expect(style.backgroundColor).toBeUndefined();
	});

	it.each([null, { kind: "local", id: "unknown-host" }] as const)("does not invent a project machine for unresolved source %j", async (source) => {
		await act(async () => {
			renderer = create(<ProjectMachineIndicator source={source} hosts={[
				{ hostId: "host-mac", name: "Mac", appearance: { symbol: "server" } },
			]} />);
		});
		expect(renderer!.root.findAllByType(MachineIdentity)).toHaveLength(0);
	});
});

describe("ProjectCard", () => {
	it("shows the owning machine name and selected symbol", () => {
		const row = {
			project: {
				id: "project-1",
				name: "AO Mobile",
				hostId: "host-1",
				hostName: "Build Laptop",
				hostAppearance: { symbol: "laptop" },
			},
			link: null,
			workers: [],
			section: "not-running",
			action: "start",
			detail: "Start one to coordinate work for this project",
			activityAt: null,
			urgency: 0,
		} as unknown as HostedProjectRow;
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<ProjectCard row={row} sourceLabel="Local" busy={false} onOpenProject={() => {}} onOrchestrator={() => {}} />);
		});

		const identity = renderer.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Build Laptop");
		expect(identity.props.appearance).toEqual({ symbol: "laptop" });
		expect(renderer.root.findAll((node) => String(node.type) === "Feather" && ["cloud", "server"].includes(node.props.name))).toHaveLength(0);
		expect(renderer.root.findAll((node) => String(node.type) === "Text" && node.props.children === "Local")).toHaveLength(0);
		act(() => renderer.unmount());
	});

	it("shows the routed machine rather than the default machine in a project header", async () => {
		await act(async () => {
			renderer = create(
				<ProjectMachineIndicator
					source={{ kind: "local", id: "host-dev" }}
					hosts={[
						{ hostId: "host-mac", name: "Mac", appearance: { symbol: "server" } },
						{ hostId: "host-dev", name: "Development Laptop", appearance: { symbol: "laptop" } },
					]}
				/>,
			);
		});

		const identity = renderer!.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Development Laptop");
		expect(identity.props.appearance).toEqual({ symbol: "laptop" });
	});
});
