import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import type { HostedProjectRow } from "./hostedRows";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./orchestrator-icon", async () => {
	const React = await import("react");
	return { OrchestratorIcon: (props: object) => React.createElement("OrchestratorIcon", props) };
});
vi.mock("./ui", async () => {
	const React = await import("react");
	return { Dot: (props: object) => React.createElement("Dot", props) };
});
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#888" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { MachineIdentity } from "./MachineIdentity";
import { ProjectCard } from "./project-card";

function projectRow(): HostedProjectRow {
	return {
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
}

describe("ProjectCard", () => {
	it("shows the owning machine name and selected symbol", () => {
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<ProjectCard row={projectRow()} busy={false} onOpenProject={() => {}} onOrchestrator={() => {}} />);
		});

		const identity = renderer.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Build Laptop");
		expect(identity.props.appearance).toEqual({ symbol: "laptop" });
	});

	// Side by side, never nested: each is its own control for a screen reader,
	// and the slot takes the pill's width instead of a fixed reservation.
	it("puts the orchestrator pill beside the row's body, not inside it", () => {
		const onOpenProject = vi.fn();
		const onOrchestrator = vi.fn();
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<ProjectCard row={projectRow()} busy={false} onOpenProject={onOpenProject} onOrchestrator={onOrchestrator} />);
		});
		const pressables = renderer.root.findAllByType("Pressable" as never);
		const body = pressables.find((node) => node.props.accessibilityHint === "Opens the project")!;
		const pill = pressables.find((node) => node.props.accessibilityLabel === "Start orchestrator, AO Mobile")!;
		expect(body.findAllByType("OrchestratorIcon" as never)).toHaveLength(0);
		expect(pill.findAllByType("OrchestratorIcon" as never)).toHaveLength(1);
		act(() => body.props.onPress());
		act(() => pill.props.onPress());
		expect(onOpenProject).toHaveBeenCalledTimes(1);
		expect(onOrchestrator).toHaveBeenCalledTimes(1);
	});

	it("shows the routed machine rather than the default machine in a project header", async () => {
		const projectCard = await import("./project-card") as typeof import("./project-card") & {
			ProjectMachineIndicator?: (props: {
				hostId: string | undefined;
				hosts: Array<{ hostId: string; name: string; appearance: { symbol: "server" | "laptop" } }>;
			}) => React.ReactNode;
		};
		expect(projectCard.ProjectMachineIndicator).toBeTypeOf("function");
		if (!projectCard.ProjectMachineIndicator) return;
		const ProjectMachineIndicator = projectCard.ProjectMachineIndicator;
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(
				<ProjectMachineIndicator
					hostId="host-dev"
					hosts={[
						{ hostId: "host-mac", name: "Mac", appearance: { symbol: "server" } },
						{ hostId: "host-dev", name: "Development Laptop", appearance: { symbol: "laptop" } },
					]}
				/>,
			);
		});

		const identity = renderer.root.findByType(MachineIdentity);
		expect(identity.props.name).toBe("Development Laptop");
		expect(identity.props.appearance).toEqual({ symbol: "laptop" });
	});
});
