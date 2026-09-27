import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "../../i18n";

const gate = vi.hoisted(() => ({ cloudEnabled: true }));
const sessionStatus = vi.hoisted(() => ({ status: "authenticated" as string }));
const providers = vi.hoisted(() => ({ available: ["nodeops", "coder"], default: "nodeops" }));
const preference = vi.hoisted(() => ({ provider: null as string | null, loading: false, saving: false, error: null as string | null, setProvider: vi.fn() }));

vi.mock("../../hooks/useCloudGate", () => ({
	useCloudGate: () => ({ cloudEnabled: gate.cloudEnabled, localEnabled: true, client: "" }),
}));
vi.mock("../../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: sessionStatus.status }),
}));
vi.mock("../../hooks/useCloudSandboxProviders", () => ({
	useCloudSandboxProviders: () => ({
		available: providers.available,
		default: providers.default,
		ready: true,
		isLoading: false,
	}),
}));
vi.mock("../../hooks/useCloudProviderPreference", () => ({ useCloudProviderPreference: () => preference }));

import { CloudProviderSection } from "./CloudProviderSection";

describe("CloudProviderSection", () => {
	beforeEach(() => {
		gate.cloudEnabled = true;
		sessionStatus.status = "authenticated";
		providers.available = ["nodeops", "coder"];
		providers.default = "nodeops";
		window.localStorage.clear();
		preference.provider = null;
		preference.saving = false;
		preference.error = null;
		preference.setProvider.mockReset();
	});

	it("renders nothing when the cloud offering is disabled", () => {
		gate.cloudEnabled = false;
		const { container } = render(<CloudProviderSection />);
		expect(container).toBeEmptyDOMElement();
	});

	it("prompts sign-in when not authenticated", () => {
		sessionStatus.status = "signedOut";
		render(<CloudProviderSection />);
		expect(screen.getByText(/sign in to ao cloud/i)).toBeInTheDocument();
	});

	it("selects the control plane provider by name", () => {
		render(<CloudProviderSection />);
		expect(screen.getByTestId("settings-section")).toHaveAttribute("data-section", "cloud-provider");
		expect(screen.getByText("NodeOps")).toBeInTheDocument();
		expect(screen.queryByText(/default/i)).not.toBeInTheDocument();
	});

	it("reflects Cloud's saved selection over the default", () => {
		preference.provider = "coder";
		render(<CloudProviderSection />);
		expect(screen.getByText("Coder")).toBeInTheDocument();
	});

	it("shows a failed save instead of claiming the next provider is applied", () => {
		preference.error = "Could not save provider";
		render(<CloudProviderSection />);
		expect(screen.getByRole("alert")).toHaveTextContent("Could not save provider");
		expect(screen.getByText("NodeOps (default)")).toBeInTheDocument();
	});

	it("disables the selector while saving", () => {
		preference.saving = true;
		render(<CloudProviderSection />);
		expect(screen.getByRole("button", { name: "Provider" })).toBeDisabled();
	});

	it("shows the single provider read-only when only one is offered", () => {
		providers.available = ["coder"];
		providers.default = "coder";
		render(<CloudProviderSection />);
		expect(screen.getByText("Coder")).toBeInTheDocument();
		// No selectable menu trigger when there is nothing to choose.
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("still shows preference errors when only one provider is offered", () => {
		providers.available = ["coder"];
		providers.default = "coder";
		preference.error = "Could not load account preference";
		render(<CloudProviderSection />);
		expect(screen.getByRole("alert")).toHaveTextContent("Could not load account preference");
	});

	it("lets a user replace a saved provider that is no longer offered", () => {
		providers.available = ["nodeops"];
		providers.default = "nodeops";
		preference.provider = "coder";
		render(<CloudProviderSection />);
		expect(screen.getByRole("button", { name: "Provider" })).toBeInTheDocument();
	});
});
