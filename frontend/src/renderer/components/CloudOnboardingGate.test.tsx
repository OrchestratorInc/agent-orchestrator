import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ status: "authenticated", preference: vi.fn() }));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: undefined }), cloudOrgQueryKey: ["cloud-org"] }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ cloudProjectsQueryKey: ["cloud-projects"], cloudSessionsQueryKey: ["cloud-sessions"] }));
vi.mock("../hooks/useProviderConnections", () => ({ useProviderConnections: () => ({ isSuccess: false }), hasValidAgentConnection: () => false }));
vi.mock("../stores/credential-dialog-store", () => ({ useCredentialDialogStore: () => vi.fn() }));
vi.mock("../hooks/useCloudProviderPreference", () => ({ useCloudProviderPreference: (options: unknown) => mocks.preference(options) }));
vi.mock("./CloudCredentialDialog", () => ({ CloudCredentialDialog: () => null }));
vi.mock("./CloudLocalSignInDialog", () => ({ CloudLocalSignInDialog: () => null }));

import { CloudOnboardingGate } from "./CloudOnboardingGate";

describe("CloudOnboardingGate", () => {
	it("mounts migration once and clears account preference cache on sign-out", () => {
		mocks.status = "authenticated";
		mocks.preference.mockClear();
		const client = new QueryClient();
		const key = ["cloud-provider-preference", "https://cloud.test", "alice"];
		client.setQueryData(key, "coder");
		const { rerender } = render(<QueryClientProvider client={client}><CloudOnboardingGate /></QueryClientProvider>);
		expect(mocks.preference).toHaveBeenCalledWith({ migrateLegacy: true });
		mocks.status = "unauthenticated";
		rerender(<QueryClientProvider client={client}><CloudOnboardingGate /></QueryClientProvider>);
		expect(client.getQueryData(key)).toBeUndefined();
	});
});
