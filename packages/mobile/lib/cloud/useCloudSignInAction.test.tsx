import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	baseUrl: "https://staging-api.aoagents.dev",
	signIn: vi.fn(async () => true), push: vi.fn(), alert: vi.fn(),
}));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: state.push }) }));
vi.mock("react-native", () => ({ Alert: { alert: state.alert } }));
vi.mock("./authStore", () => ({ useCloudAuth: () => ({ signInWithWorkOS: state.signIn }) }));
vi.mock("./config", () => ({ get CLOUD_BASE_URL() { return state.baseUrl; } }));
import { useCloudSignInAction } from "./useCloudSignInAction";

let renderer: ReactTestRenderer | undefined;
let action: ReturnType<typeof useCloudSignInAction>;
function Probe() { action = useCloudSignInAction(); return null; }

beforeEach(async () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.clearAllMocks(); state.baseUrl = "https://staging-api.aoagents.dev";
	state.signIn.mockResolvedValue(true);
	await act(async () => { renderer = create(<Probe />); });
});
afterEach(async () => { await act(async () => { renderer?.unmount(); }); renderer = undefined; vi.unstubAllGlobals(); });

it("opens the hosted browser without a confirmation route", async () => {
	let authenticated = false;
	await act(async () => { authenticated = await action.signIn(); });
	expect(authenticated).toBe(true); expect(state.signIn).toHaveBeenCalledOnce(); expect(state.push).not.toHaveBeenCalled();
});
it("keeps browser cancellation on the originating screen", async () => {
	state.signIn.mockResolvedValueOnce(false);
	let authenticated = true;
	await act(async () => { authenticated = await action.signIn(); });
	expect(authenticated).toBe(false); expect(state.push).not.toHaveBeenCalled(); expect(state.alert).not.toHaveBeenCalled();
	expect(action.busy).toBe(false);
});
it("shows one error alert and resets busy after a hosted sign-in failure", async () => {
	state.signIn.mockRejectedValueOnce(new Error("Unable to open sign-in"));
	let authenticated = true;
	await act(async () => { authenticated = await action.signIn(); });
	expect(authenticated).toBe(false);
	expect(state.alert).toHaveBeenCalledExactlyOnceWith("Couldn’t sign in", "Unable to open sign-in");
	expect(state.push).not.toHaveBeenCalled();
	expect(action.busy).toBe(false);
});
it("coalesces repeat taps until the hosted browser finishes", async () => {
	let finish!: (value: boolean) => void;
	state.signIn.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
	let pending!: Promise<boolean>;
	await act(async () => { pending = action.signIn(); });
	expect(action.busy).toBe(true);
	expect(await action.signIn()).toBe(false); expect(state.signIn).toHaveBeenCalledOnce();
	await act(async () => { finish(false); await pending; });
	expect(action.busy).toBe(false);
});
it("does not return late browser success to an unmounted screen", async () => {
	let finish!: (value: boolean) => void;
	state.signIn.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
	let pending!: Promise<boolean>;
	await act(async () => { pending = action.signIn(); });
	await act(async () => { renderer?.unmount(); renderer = undefined; });
	finish(true);
	expect(await pending).toBe(false); expect(state.alert).not.toHaveBeenCalled();
});
it("does not show a late failure alert after the originating screen unmounts", async () => {
	let fail!: (reason: Error) => void;
	state.signIn.mockImplementationOnce(() => new Promise<boolean>((_resolve, reject) => { fail = reject; }));
	let pending!: Promise<boolean>;
	await act(async () => { pending = action.signIn(); });
	await act(async () => { renderer?.unmount(); renderer = undefined; });
	fail(new Error("Unable to open sign-in"));
	expect(await pending).toBe(false);
	expect(state.alert).not.toHaveBeenCalled();
	expect(state.push).not.toHaveBeenCalled();
});
it("uses the real credential form only for local development", async () => {
	state.baseUrl = "http://localhost:8080";
	let authenticated = true;
	await act(async () => { authenticated = await action.signIn(); });
	expect(authenticated).toBe(false); expect(state.push).toHaveBeenCalledWith("/sheets/cloud-signin");
	expect(state.signIn).not.toHaveBeenCalled();
});
