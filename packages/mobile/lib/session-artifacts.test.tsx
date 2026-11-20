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
	// The board poll's last failure: none after an app switch.
	error: null as string | null,
	errorStatus: null as number | null,
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
		// What an app switch leaves behind: never read as offline (see machineState).
		connection: "closed",
		unreachable: fixtures.unreachable,
		paired: fixtures.paired,
		error: fixtures.error,
		errorStatus: fixtures.errorStatus,
		config: null,
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
	fixtures.error = null;
	fixtures.errorStatus = null;
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

	// A password change or a removed device: the machine answered 401/403, the
	// board's poll stopped for good, and the screen used to spin forever.
	it.each([401, 403])("says the machine rejected this phone after a %s, and offers a new code for it", async (status) => {
		fixtures.error = "Your machine rejected this phone's password.";
		fixtures.errorStatus = status;
		await openArtifacts();
		expect(nodes("ActivityIndicator")).toHaveLength(0);
		expect(titles()).toEqual(["Your machine rejected this phone"]);
		const buttons = [nodes("EmptyState")[0]!.props.action].flat();
		// The repair only: no Retry, which would spend an attempt toward the lockout.
		expect(buttons.map((button) => button.props.title)).toEqual(["Scan new code"]);
		act(() => buttons[0]!.props.onPress());
		expect(fixtures.router.push).toHaveBeenCalledWith({ pathname: "/pair", params: { repairHostId: "host-1" } });
	});

	it("says the machine is pausing this phone under the lockout, with nothing to press", async () => {
		fixtures.error = "Too many attempts.";
		fixtures.errorStatus = 429;
		await openArtifacts();
		expect(titles()).toEqual(["Too many attempts"]);
		expect(nodes("EmptyState")[0]!.props.action).toBeUndefined();
	});

	it("keeps waiting for an answered error the poll keeps going through", async () => {
		fixtures.error = "Something went wrong on your machine.";
		fixtures.errorStatus = 500;
		await openArtifacts();
		expect(titles()).toEqual([]);
		expect(nodes("ActivityIndicator")).toHaveLength(1);
	});
});
