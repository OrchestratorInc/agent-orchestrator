import type { ComponentType } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { DefaultCloudSandboxRow, DefaultCloudSandboxSection } from "./DefaultCloudSandboxRow";

const mocks = vi.hoisted(() => ({
	client: { putOrgSandboxProvider: vi.fn() },
	offering: {
		providers: ["coder", "freestyle"],
		defaultProvider: "coder",
		offersProviderChoice: true,
		canManageBilling: true,
		org: { id: "org-1" } as { id: string } | undefined,
	},
	cloudEnabled: true,
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: mocks.cloudEnabled }) }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true }) }));
vi.mock("../../hooks/useCloudOffering", () => ({ useCloudOffering: () => mocks.offering }));

function renderRow(Row: ComponentType = DefaultCloudSandboxRow) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	render(
		<QueryClientProvider client={queryClient}>
			<Row />
		</QueryClientProvider>,
	);
	return { invalidate };
}

describe("DefaultCloudSandboxRow", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		vi.clearAllMocks();
		mocks.cloudEnabled = true;
		Object.assign(mocks.offering, {
			providers: ["coder", "freestyle"], defaultProvider: "coder", offersProviderChoice: true,
			canManageBilling: true, org: { id: "org-1" },
		});
	});

	it("shows the org's default and saves a new one", async () => {
		mocks.client.putOrgSandboxProvider.mockResolvedValue({ providers: ["coder", "freestyle"], defaultProvider: "freestyle" });
		const { invalidate } = renderRow();
		const trigger = screen.getByRole("button", { name: "Default cloud sandbox" });
		expect(trigger).toHaveTextContent("Dev workspace (Coder)");
		await userEvent.click(trigger);
		await userEvent.click(screen.getByRole("menuitem", { name: "Instant" }));
		await waitFor(() => expect(mocks.client.putOrgSandboxProvider).toHaveBeenCalledWith("org-1", "freestyle"));
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-me"] }));
	});

	it("shows a single-provider org its one sandbox, fixed", () => {
		Object.assign(mocks.offering, { providers: ["coder"], defaultProvider: "coder", offersProviderChoice: false });
		renderRow();
		const trigger = screen.getByRole("button", { name: "Default cloud sandbox" });
		expect(trigger).toHaveTextContent("Dev workspace (Coder)");
		expect(trigger).toBeDisabled();
		expect(screen.getByText("Where your cloud sessions run. Only one sandbox is available.")).toBeInTheDocument();
		expect(screen.queryByText("Only organization admins can change this.")).not.toBeInTheDocument();
	});

	it("is hidden when the org has no cloud sandbox", () => {
		Object.assign(mocks.offering, { providers: [], defaultProvider: "", offersProviderChoice: false });
		renderRow();
		expect(screen.queryByRole("button", { name: "Default cloud sandbox" })).not.toBeInTheDocument();
	});

	it("is hidden when cloud is off", () => {
		mocks.cloudEnabled = false;
		renderRow();
		expect(screen.queryByRole("button", { name: "Default cloud sandbox" })).not.toBeInTheDocument();
	});

	it("is read-only for members who are not admins", () => {
		mocks.offering.canManageBilling = false;
		renderRow();
		expect(screen.getByRole("button", { name: "Default cloud sandbox" })).toBeDisabled();
		expect(screen.getByText("Only organization admins can change this.")).toBeInTheDocument();
	});

	it("says so when saving fails", async () => {
		mocks.client.putOrgSandboxProvider.mockRejectedValue(new Error("boom"));
		renderRow();
		await userEvent.click(screen.getByRole("button", { name: "Default cloud sandbox" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Instant" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't save the default sandbox. Try again.");
	});

	describe("as its own Cloud section", () => {
		it("shows the row under a Cloud heading", () => {
			renderRow(DefaultCloudSandboxSection);
			expect(screen.getByRole("heading", { name: "Cloud" })).toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Default cloud sandbox" })).toBeEnabled();
		});

		it.each([
			["cloud is off", () => { mocks.cloudEnabled = false; }],
			["the org has no cloud sandbox", () => { Object.assign(mocks.offering, { providers: [], offersProviderChoice: false }); }],
			["the org runs on a single provider", () => { Object.assign(mocks.offering, { providers: ["coder"], offersProviderChoice: false }); }],
			["the user is not signed in to an org", () => { mocks.offering.org = undefined; }],
		])("leaves the whole section out when %s", (_case, arrange) => {
			arrange();
			renderRow(DefaultCloudSandboxSection);
			expect(screen.queryByRole("heading", { name: "Cloud" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Default cloud sandbox" })).not.toBeInTheDocument();
		});
	});
});
