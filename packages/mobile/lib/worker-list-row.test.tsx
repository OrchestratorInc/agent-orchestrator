import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DashboardSession } from "./api";
import { darkTheme } from "./theme";

const fixtures = vi.hoisted(() => ({
	push: vi.fn(),
	tap: vi.fn(),
}));

vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixtures.push }) }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return {
		useTheme: () => darkTheme,
		useThemedStyles: (factory: (theme: typeof darkTheme) => unknown) => factory(darkTheme),
	};
});
vi.mock("./AgentLogo", async () => {
	const React = await import("react");
	return { AgentLogo: (props: object) => React.createElement("AgentLogo", props) };
});
vi.mock("./worker-row-actions", () => ({ WorkerRowActions: () => null }));
vi.mock("./worker-row-interaction", async () => {
	const React = await import("react");
	return {
		WorkerRowInteraction: ({ children, ...props }: { children?: React.ReactNode }) =>
			React.createElement("WorkerRowInteraction", props, children),
	};
});
vi.mock("./openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("./ui", () => ({ Spinning: () => null }));
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Keyboard: { dismiss: vi.fn() },
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		TextInput: host("TextInput"),
		View: host("View"),
	};
});

import { WorkerListRow } from "./worker-list-row";

const session: DashboardSession = {
	id: "worker-1",
	projectId: "project-a",
	status: "working",
	mode: "chat",
	branch: "ao/worker-1",
	issueId: null,
	issueTitle: null,
	userPrompt: null,
	displayName: "Cloud worker",
	summary: null,
	createdAt: "2026-09-21T00:00:00Z",
	lastActivityAt: "2026-09-21T00:00:00Z",
};

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.push.mockReset();
	fixtures.tap.mockReset();
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

describe("Cloud worker row", () => {
	it("shows the Cloud source as a readable outlined capsule in dark mode", async () => {
		const props = { interactionMode: "open-only", session, source: { kind: "cloud", id: "org-1" }, projectName: "Tap" } as unknown as Parameters<typeof WorkerListRow>[0];
		await act(async () => { renderer = create(<WorkerListRow {...props} />); });
		const badge = renderer!.root.findAll((node) => String(node.type) === "Text" && node.props.children === "Cloud")[0];
		expect(badge).toBeDefined();
		expect(badge.props.style).toMatchObject({ color: darkTheme.textPrimary, borderColor: darkTheme.borderStrong, borderWidth: 1, borderRadius: 999, minHeight: 16 });
		expect(badge.props.style.height).toBeUndefined();
		expect(badge.props.style.backgroundColor).toBeUndefined();
	});

	it("opens the session without mounting Local mutation interactions", async () => {
		const props = { interactionMode: "open-only", session, source: { kind: "cloud", id: "org-1" }, projectName: "Tap" } as unknown as Parameters<typeof WorkerListRow>[0];
		await act(async () => { renderer = create(<WorkerListRow {...props} />); });

		const root = renderer!.root;
		expect(root.findAll((node) => String(node.type) === "WorkerRowInteraction")).toHaveLength(0);
		const row = root.find((node) => String(node.type) === "Pressable");
		act(() => row.props.onPress());

		expect(fixtures.tap).toHaveBeenCalledOnce();
		expect(fixtures.push).toHaveBeenCalledWith({
			pathname: "/session/[id]",
			params: { id: "worker-1", projectId: "project-a", source: "cloud", sourceId: "org-1" },
		});
	});
});
