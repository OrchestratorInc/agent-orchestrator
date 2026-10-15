import { act, create } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ platform: "android", baseUrl: "https://staging-api.aoagents.dev", from: undefined as string | undefined }));
vi.mock("react-native", () => ({ Platform: { get OS() { return state.platform; } } }));
vi.mock("expo-router", () => ({ Stack: { Screen: "StackScreen" }, Redirect: "Redirect", useRouter: () => ({}), useLocalSearchParams: () => ({ from: state.from }) }));
vi.mock("./config", () => ({ get CLOUD_BASE_URL() { return state.baseUrl; } }));
vi.mock("../CloudSignInSheet", () => ({ CloudSignInSheet: "CloudSignInSheet" }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));
import CloudSignInSheetRoute from "../../app/sheets/cloud-signin";

afterEach(() => { vi.unstubAllGlobals(); });

it.each([
	["android", "http://localhost:8080", [0.6, 0.95]],
	["ios", "http://localhost:8080", "fitToContents"],
])("sizes %s sign-in at %s for its actual input controls", async (platform, baseUrl, detents) => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("__DEV__", true);
	state.platform = platform; state.baseUrl = baseUrl; state.from = undefined;
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<CloudSignInSheetRoute />); });
	expect(renderer.root.findByType("StackScreen" as never).props.options.sheetAllowedDetents).toEqual(detents);
	await act(async () => { renderer.unmount(); });
});

it.each([
	["https://staging-api.aoagents.dev", undefined, "/"],
	["https://api.aoagents.dev", undefined, "/"],
	["https://staging-api.aoagents.dev", "onboarding", "/onboarding"],
])("does not show the confirmation sheet for hosted %s sign-in", async (baseUrl, from, href) => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	state.baseUrl = baseUrl; state.from = from;
	let renderer!: ReturnType<typeof create>;
	await act(async () => { renderer = create(<CloudSignInSheetRoute />); });
	expect(renderer.root.findAllByType("CloudSignInSheet" as never)).toHaveLength(0);
	expect(renderer.root.findByType("Redirect" as never).props.href).toBe(href);
	await act(async () => { renderer.unmount(); });
});
