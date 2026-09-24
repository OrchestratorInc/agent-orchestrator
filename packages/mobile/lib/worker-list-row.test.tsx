import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { DashboardSession } from "./api";

const fixtures = vi.hoisted(() => ({
	push: vi.fn(),
	tap: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixtures.push }) }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => unknown) => factory(fixtures.theme),
}));
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
vi.mock("@expo/vector-icons", async () => {
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
	it("opens the session without mounting Local mutation interactions", async () => {
		const props = { interactionMode: "open-only", session, projectName: "Tap" } as unknown as Parameters<typeof WorkerListRow>[0];
		await act(async () => { renderer = create(<WorkerListRow {...props} />); });

		const root = renderer!.root;
		expect(root.findAll((node) => String(node.type) === "WorkerRowInteraction")).toHaveLength(0);
		const row = root.find((node) => String(node.type) === "Pressable");
		act(() => row.props.onPress());

		expect(fixtures.tap).toHaveBeenCalledOnce();
		expect(fixtures.push).toHaveBeenCalledWith({
			pathname: "/session/[id]",
			params: { id: "worker-1", projectId: "project-a" },
		});
	});
});
