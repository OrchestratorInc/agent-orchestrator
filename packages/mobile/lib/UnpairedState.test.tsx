import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	signedIn: true as boolean | null,
	orgLoading: false,
	orgError: "Network request failed" as string | null,
	retryOrgResolution: vi.fn(async () => {}),
	push: vi.fn(),
}));
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => state }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: state.push }) }));
vi.mock("./ui", () => ({ Button: "Button", EmptyState: "EmptyState" }));
import { CloudUnreadyState } from "./UnpairedState";

beforeEach(() => {
	state.signedIn = true;
	state.orgLoading = false;
	state.orgError = "Network request failed";
	vi.clearAllMocks();
});

describe("CloudUnreadyState", () => {
	it("shows the workspace failure and lets a signed-in account retry", async () => {
		const element = CloudUnreadyState();
		expect(element.props.message).toContain("Network request failed");
		expect(element.props.action.props.title).toBe("Retry");
		await element.props.action.props.onPress();
		expect(state.retryOrgResolution).toHaveBeenCalledOnce();
	});

	it("shows loading while organization resolution is pending", () => {
		state.orgLoading = true;
		state.orgError = null;
		const element = CloudUnreadyState();
		expect(element.props.title).toMatch(/loading/i);
		expect(element.props.action).toBeUndefined();
	});

	it("continues to route a signed-out account to sign in", () => {
		state.signedIn = false;
		const element = CloudUnreadyState();
		element.props.action.props.onPress();
		expect(state.push).toHaveBeenCalledWith("/sheets/cloud-signin");
	});
});
