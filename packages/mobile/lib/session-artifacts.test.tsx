import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	params: { sessionId: "session-1", hostId: "host-1" },
	navigation: { setOptions: vi.fn() },
	router: { push: vi.fn(), navigate: vi.fn() },
	// No backend yet: a cold start from a notification, or the reconnect after an app switch.
	backend: null as object | null,
	unreachable: false,
	paired: true as boolean | null,
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"), FlatList: host("FlatList"), Pressable: host("Pressable"),
		RefreshControl: host("RefreshControl"), Text: host("Text"), View: host("View"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		useWindowDimensions: () => ({ width: 390, height: 844 }),
	};
});
vi.mock("expo-router", () => ({
	useLocalSearchParams: () => fixtures.params,
	useNavigation: () => fixtures.navigation,
	useRouter: () => fixtures.router,
}));
vi.mock("./ArtifactPoster", () => ({ ArtifactPoster: () => null }));
vi.mock("./artifact-menu", () => ({ useArtifactActions: () => ({ openMenu: vi.fn(), menu: null }) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./useArtifactsBackend", () => ({ useArtifactsBackend: () => ({ backend: fixtures.backend, cloud: false }) }));
vi.mock("./useSessionArtifacts", () => ({
	useSessionArtifacts: () => ({ artifacts: null, loading: false, error: undefined, reload: vi.fn(), markDeleted: vi.fn() }),
}));
vi.mock("./store", () => ({
	HostScope: ({ children }: { children: React.ReactNode }) => children,
	useApp: () => ({
		currentHostId: "host-1",
		// What an app switch leaves behind: never read as offline (see machineOffline).
		connection: "closed",
		unreachable: fixtures.unreachable,
		paired: fixtures.paired,
	}),
}));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#888" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("./ui", async () => {
	const React = await import("react");
	return { Button: (props: object) => React.createElement("Button", props), EmptyState: (props: object) => React.createElement("EmptyState", props) };
});

import SessionArtifactsScreen from "../app/artifacts/[sessionId]";

let screen: ReactTestRenderer;
const nodes = (name: string) => screen.root.findAll((node) => (node.type as unknown) === name);
const titles = () => nodes("EmptyState").map((node) => node.props.title);

async function openArtifacts() {
	await act(async () => { screen = create(<SessionArtifactsScreen />); });
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.backend = null;
	fixtures.unreachable = false;
	fixtures.paired = true;
});

afterEach(() => {
	act(() => { screen?.unmount(); });
	vi.unstubAllGlobals();
});

describe("session artifacts while the connection is away", () => {
	it("waits for the reconnect instead of saying the machine is offline", async () => {
		await openArtifacts();
		expect(titles()).not.toContain("Machine offline");
		expect(nodes("ActivityIndicator")).toHaveLength(1);
	});

	it("waits while a cold start has not read the pairing yet", async () => {
		fixtures.paired = null;
		await openArtifacts();
		expect(titles()).not.toContain("Machine offline");
	});

	it("says the machine is offline once it really is, or when no machine is paired", async () => {
		fixtures.unreachable = true;
		await openArtifacts();
		expect(titles()).toEqual(["Machine offline"]);
		act(() => { screen.unmount(); });
		fixtures.unreachable = false;
		fixtures.paired = false;
		await openArtifacts();
		expect(titles()).toEqual(["Machine offline"]);
	});
});
