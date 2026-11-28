import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	params: { url: "https://orchestrator.inc/artifact/shared-report" } as { url: string | string[] },
	router: { canGoBack: () => false, back: vi.fn(), replace: vi.fn() },
}));
vi.mock("react-native", () => ({ Platform: { OS: "ios" }, View: "View", StyleSheet: { create: (value: unknown) => value } }));
vi.mock("expo-router", () => ({
	Stack: { Screen: "StackScreen" },
	useLocalSearchParams: () => state.params,
	useRouter: () => state.router,
}));
vi.mock("react-native-webview", () => ({ WebView: "WebView" }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./useHeaderOverlap", () => ({ useGlassBarInset: () => 0 }));
import SharedArtifactScreen from "../app/shared-artifact";
let screen: ReactTestRenderer;
const nodes = (name: string) => screen.root.findAll((node) => (node.type as unknown) === name);
beforeEach(() => {
	vi.clearAllMocks();
	state.params = { url: "https://orchestrator.inc/artifact/shared-report" };
});
afterEach(() => { if (screen) act(() => screen.unmount()); });
describe("shared artifact screen", () => {
	it("loads only the published page, with no API authorization headers", () => {
		act(() => { screen = create(<SharedArtifactScreen />); });
		const web = nodes("WebView")[0].props;
		expect(web.source).toEqual({ uri: `${state.params.url}?inApp=1` });
		expect(web.allowFileAccess).toBe(false);
		expect(web.originWhitelist).toEqual(["*"]);
		expect(web.onShouldStartLoadWithRequest({ url: "https://auth.example.com" })).toBe(true);
		for (const url of ["file:///private", "javascript:alert(1)", "http://localhost", "ao-app://callback"]) {
			expect(web.onShouldStartLoadWithRequest({ url })).toBe(false);
		}
	});
	it("refuses invalid and ambiguous targets", () => {
		for (const url of ["file:///artifact/report", "https://example.com/not-an-artifact", ["https://example.com/artifact/a", "https://example.com/artifact/b"]]) {
			state.params = { url };
			act(() => { screen = create(<SharedArtifactScreen />); });
			expect(nodes("WebView")).toHaveLength(0);
			expect(nodes("EmptyState")).toHaveLength(1);
			act(() => screen.unmount());
		}
	});
	it("can open another shared link after a load failure", () => {
		act(() => { screen = create(<SharedArtifactScreen />); });
		act(() => nodes("WebView")[0].props.onError());
		expect(nodes("EmptyState")).toHaveLength(1);
		state.params.url = "https://orchestrator.inc/artifact/another-report";
		act(() => screen.update(<SharedArtifactScreen />));
		expect(nodes("WebView")[0].props.source.uri).toBe(`${state.params.url}?inApp=1`);
	});
	it("lets a cold-start visitor close the document without a back stack", () => {
		act(() => { screen = create(<SharedArtifactScreen />); });
		act(() => nodes("Button")[0].props.onPress());
		expect(state.router.replace).toHaveBeenCalledWith("/");
	});
});
