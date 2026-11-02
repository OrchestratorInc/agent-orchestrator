import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, test, vi } from "vitest";
import { AccountHostSyncError } from "../lib/account-remote-hosts";
import { apiClient } from "../lib/api-client";
import { linkThisComputer } from "../lib/link-this-computer";

// Signing in while Connect Mobile serves this computer links it, so it gets
// its fixed address without the user turning on Allow access as well.
const fake = vi.hoisted(() => ({
	signedIn: false,
	status: { enabled: true, noPublicTunnel: false, accountLinked: false, hostId: "h_this", password: "pw-1", url: "https://quick.trycloudflare.com:443" },
}));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ session: fake.signedIn ? { user: { id: "user_01" } } : null }) }));
vi.mock("./useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.test" } }) }));
vi.mock("../lib/link-this-computer", () => ({ linkThisComputer: vi.fn(async () => ({ hostId: "h_this", added: true })) }));
vi.mock("../lib/api-client", () => ({ apiClient: { POST: vi.fn(async () => ({ data: {} })) } }));
vi.mock("../components/settings/ConnectMobileContent", () => ({
	fetchMobileStatus: async () => ({ ...fake.status }),
	tunnelUrl: (status: { url: string }) => status.url,
}));

import { useLinkThisComputerOnSignIn } from "./useLinkThisComputerOnSignIn";

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;

beforeEach(() => {
	vi.mocked(linkThisComputer).mockClear();
	vi.mocked(apiClient.POST).mockClear();
	fake.signedIn = false;
	Object.assign(fake.status, { enabled: true, noPublicTunnel: false, accountLinked: false });
});

test("signing in links a computer Connect Mobile already serves", async () => {
	const { rerender } = renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	expect(linkThisComputer).not.toHaveBeenCalled();
	fake.signedIn = true;
	rerender();
	await waitFor(() => expect(linkThisComputer).toHaveBeenCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: undefined }));
	// The connector is asked again once the listing landed, so it moves at once.
	await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/mobile/remote-access"));
	rerender();
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).toHaveBeenCalledTimes(1); // once per sign-in
});

test("an AO Cloud without fixed addresses lists it at the tunnel's address", async () => {
	vi.mocked(linkThisComputer).mockResolvedValueOnce({ hostId: "", added: false });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: "https://quick.trycloudflare.com:443" }));
});

test("an account with no fixed address left lists it at the tunnel's address", async () => {
	vi.mocked(linkThisComputer).mockRejectedValueOnce(new AccountHostSyncError("AO Cloud host sync failed (409).", 409));
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await waitFor(() => expect(linkThisComputer).toHaveBeenLastCalledWith("https://cp.example.test", "user_01", "h_this", "pw-1", { ifUnlisted: true, url: "https://quick.trycloudflare.com:443" }));
});

test("a computer an account already holds is left alone", async () => {
	// Signing in to another account must not take it from the first, and one
	// its account removed on another device must not be listed again.
	Object.assign(fake.status, { accountLinked: true });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).not.toHaveBeenCalled();
});

test("a computer without remote access is not linked", async () => {
	Object.assign(fake.status, { noPublicTunnel: true });
	fake.signedIn = true;
	renderHook(() => useLinkThisComputerOnSignIn(), { wrapper });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(linkThisComputer).not.toHaveBeenCalled();
});
