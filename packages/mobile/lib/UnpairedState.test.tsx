import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	signedIn: true as boolean | null,
	orgLoading: false,
	orgError: "Network request failed" as string | null,
	retryOrgResolution: vi.fn(async () => {}),
	signInWithWorkOS: vi.fn(async () => true),
	push: vi.fn(),
	alert: vi.fn(),
}));
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => state }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: state.push }) }));
vi.mock("react-native", () => ({ Alert: { alert: state.alert } }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
import { CloudUnreadyState } from "./UnpairedState";

let renderer: ReactTestRenderer | undefined;

async function mount() {
	await act(async () => { renderer = create(<CloudUnreadyState />); });
	return renderer!.root.find((node) => String(node.type) === "EmptyState");
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	state.signedIn = true;
	state.orgLoading = false;
	state.orgError = "Network request failed";
	state.signInWithWorkOS.mockResolvedValue(true);
	vi.clearAllMocks();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("CloudUnreadyState", () => {
	it("shows the workspace failure and lets a signed-in account retry", async () => {
		const element = await mount();
		expect(element.props.message).toContain("Network request failed");
		const button = element.props.action;
		expect(button.props.title).toBe("Retry");
		await act(async () => { await button.props.onPress(); });
		expect(state.retryOrgResolution).toHaveBeenCalledOnce();
	});

	it("shows loading while organization resolution is pending", async () => {
		state.orgLoading = true;
		state.orgError = null;
		const element = await mount();
		expect(element.props.title).toMatch(/loading/i);
		expect(element.props.action).toBeUndefined();
	});

	it("launches hosted AuthKit directly for a signed-out production account", async () => {
		state.signedIn = false;
		const element = await mount();
		const button = element.props.action;
		await act(async () => { await button.props.onPress(); });

		expect(state.signInWithWorkOS).toHaveBeenCalledOnce();
		expect(state.push).not.toHaveBeenCalled();
	});
});
