import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OrchestratorProjectRow } from "./orchestratorView";
import { darkTheme, lightTheme } from "./theme";

const fixture = vi.hoisted(() => ({ scheme: "dark" as "dark" | "light" }));

vi.mock("./ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("./theme");
	const current = () => fixture.scheme === "dark" ? darkTheme : lightTheme;
	return { useTheme: current, useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(current()) };
});
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

import { ProjectCard } from "./project-card";

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

describe("project environment badge", () => {
	it.each([
		["dark", darkTheme],
		["light", lightTheme],
	] as const)("shows borderless Local and Cloud icons in %s mode", async (scheme, theme) => {
		fixture.scheme = scheme;
		for (const [sourceLabel, iconName] of [["Local", "server"], ["Cloud", "cloud"]] as const) {
			await act(async () => { renderer = create(<ProjectCard row={row} sourceLabel={sourceLabel} busy={false} onOpenProject={vi.fn()} />); });
			const badge = renderer!.root.findAll((node) => String(node.type) === "View" && node.props.accessibilityLabel === `${sourceLabel} environment`)[0];
			expect(badge).toBeDefined();
			expect(badge.props.style.borderColor).toBeUndefined();
			expect(badge.props.style.borderWidth).toBeUndefined();
			expect(badge.props.style.backgroundColor).toBeUndefined();
			expect(badge.findAll((node) => String(node.type) === "Feather" && node.props.name === iconName && node.props.color === theme.textPrimary)).toHaveLength(1);
			expect(renderer!.root.findAll((node) => String(node.type) === "Text" && node.props.children === sourceLabel)).toHaveLength(0);
			const rowButton = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityRole === "button")[0];
			expect(rowButton.props.accessibilityLabel).toContain(sourceLabel);
			await act(async () => renderer?.unmount());
			renderer = undefined;
		}
	});
});
