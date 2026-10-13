import { act, create } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ platform: "android", baseUrl: "https://staging-api.aoagents.dev" }));
vi.mock("react-native", () => ({ Platform: { get OS() { return state.platform; } } }));
vi.mock("expo-router", () => ({ Stack: { Screen: "StackScreen" }, useRouter: () => ({}), useLocalSearchParams: () => ({}) }));
vi.mock("./config", () => ({ get CLOUD_BASE_URL() { return state.baseUrl; } }));
vi.mock("../CloudSignInSheet", () => ({ CloudSignInSheet: () => null }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));
import CloudSignInSheetRoute from "../../app/sheets/cloud-signin";

afterEach(() => { vi.unstubAllGlobals(); });

it.each([
	["android", "https://staging-api.aoagents.dev", "fitToContents"],
	["android", "https://api.aoagents.dev", "fitToContents"],
	["ios", "https://staging-api.aoagents.dev", "fitToContents"],
	["android", "http://localhost:8080", [0.6, 0.95]],
	["ios", "http://localhost:8080", "fitToContents"],
])("sizes %s sign-in at %s for its actual input controls", async (platform, baseUrl, detents) => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("__DEV__", true);
	state.platform = platform; state.baseUrl = baseUrl;
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<CloudSignInSheetRoute />); });
	expect(renderer.root.findByType("StackScreen" as never).props.options.sheetAllowedDetents).toEqual(detents);
	await act(async () => { renderer.unmount(); });
});
