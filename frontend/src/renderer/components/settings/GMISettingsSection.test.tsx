import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { apiClient } from "../../lib/api-client";
import { GMISettingsSection } from "./GMISettingsSection";

// The GMI key, as the daemon reports it: no value, only its state.
const gmiKey = { name: "GMI_API_KEY", label: "GMI Cloud", agents: [], configured: false, hint: "", checkable: true, device: false, deviceAvailable: true };

function renderSection() {
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<GMISettingsSection />
		</QueryClientProvider>,
	);
}

/** The daemon's answer to each request the section makes. */
function mockApi(overrides: {
	configured?: boolean;
	check?: string;
} = {}) {
	let configured = overrides.configured ?? false;
	const get = vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
		if (path === "/api/v1/agents/provider-keys") {
			return { data: { keys: [{ ...gmiKey, configured, hint: configured ? "beef" : "" }] } } as never;
		}
		return { data: undefined } as never;
	});
	const post = vi.spyOn(apiClient, "POST").mockImplementation(async (path) => {
		if (path === "/api/v1/agents/provider-keys/{name}/check") return { data: { status: overrides.check ?? "valid" } } as never;
		return { data: undefined } as never;
	});
	const put = vi.spyOn(apiClient, "PUT").mockImplementation(async () => {
		configured = true;
		return { data: { ...gmiKey, configured: true, hint: "beef" } } as never;
	});
	const remove = vi.spyOn(apiClient, "DELETE").mockImplementation(async () => {
		configured = false;
		return { data: { ...gmiKey, configured: false } } as never;
	});
	return { get, post, put, remove };
}

/** The key field, once the daemon's list of keys has arrived. */
async function keyField() {
	const input = await screen.findByLabelText("API key");
	await waitFor(() => expect(input).toBeEnabled());
	return input;
}

describe("GMISettingsSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		vi.restoreAllMocks();
	});

	it("saves a typed key for AO alone, without sharing it with other apps", async () => {
		const api = mockApi();
		const user = userEvent.setup();
		renderSection();

		await user.type(await keyField(), "gmi-secret");
		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1));
		expect(api.put).toHaveBeenCalledWith("/api/v1/agents/provider-keys/{name}", {
			params: { path: { name: "GMI_API_KEY" } },
			body: { secret: "gmi-secret", device: false },
		});
		// The typed key never leaves the field it was typed in.
		await waitFor(() => expect(screen.getByLabelText("API key")).toHaveValue(""));
		expect(await screen.findByText("Saved, ending beef")).toBeInTheDocument();
	});

	it("keeps a key the provider rejects out of AO's store", async () => {
		const api = mockApi({ check: "invalid" });
		const user = userEvent.setup();
		renderSection();

		await user.type(await keyField(), "wrong-key");
		await user.click(screen.getByRole("button", { name: "Save" }));

		expect(await screen.findByText("The provider rejected this key.")).toBeInTheDocument();
		expect(api.put).not.toHaveBeenCalled();
		// The typed key stays, so it can be corrected.
		expect(screen.getByLabelText("API key")).toHaveValue("wrong-key");
	});

	it("removes a saved key only after a second click", async () => {
		const api = mockApi({ configured: true });
		const user = userEvent.setup();
		renderSection();

		await user.click(await screen.findByRole("button", { name: "Remove" }));
		expect(api.remove).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Confirm remove" }));

		await waitFor(() => expect(api.remove).toHaveBeenCalledWith("/api/v1/agents/provider-keys/{name}", {
			params: { path: { name: "GMI_API_KEY" } },
		}));
		await waitFor(() => expect(screen.queryByRole("button", { name: "Remove" })).toBeNull());
	});

	it("says where to get a key while none is saved", async () => {
		mockApi();
		renderSection();
		expect(await screen.findByRole("button", { name: "Get a key" })).toBeInTheDocument();
	});
});
