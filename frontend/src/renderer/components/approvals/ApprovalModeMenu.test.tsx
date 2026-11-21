import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { approvalPolicyQueryKey } from "../../lib/approvals";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { ApprovalModeMenuItems, ApprovalModeMenuRow } from "./ApprovalModeMenu";

const remote = vi.hoisted(() => ({ GET: vi.fn(), PUT: vi.fn() }));
vi.mock("../../lib/host-clients", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../lib/host-clients")>();
	return { ...actual, clientForSessionHost: (hostId?: string) => (hostId === "box" ? remote : actual.clientForSessionHost(hostId)) };
});

const policy = (mode: string) => ({ data: { mode, defaultMode: "risky", sites: [] } });

describe("ApprovalModeMenu", () => {
	afterEach(() => vi.restoreAllMocks());

	it("shows and changes this session's mode without a query provider", async () => {
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue(policy("risky") as never);
		const put = vi.spyOn(apiClient, "PUT").mockResolvedValue(policy("every-submit") as never);
		const onBack = vi.fn();
		render(
			<DropdownMenu open>
				<DropdownMenuTrigger>menu</DropdownMenuTrigger>
				<DropdownMenuContent>
					<ApprovalModeMenuRow sessionId="ao-7" onOpen={vi.fn()} />
					<ApprovalModeMenuItems sessionId="ao-7" onBack={onBack} />
				</DropdownMenuContent>
			</DropdownMenu>,
		);
		expect(get).toHaveBeenCalledWith("/api/v1/approvals/policy", { params: { query: { sessionId: "ao-7" } } });
		await waitFor(() => expect(screen.getAllByText("Risky actions")).toHaveLength(2));
		fireEvent.click(screen.getByRole("menuitem", { name: "Every form submission" }));
		await waitFor(() =>
			expect(put).toHaveBeenCalledWith("/api/v1/approvals/policy", { body: { sessionId: "ao-7", mode: "every-submit" } }),
		);
	});

	it("reads and writes a remote session's mode on its own host", async () => {
		const localGet = vi.spyOn(apiClient, "GET");
		const localPut = vi.spyOn(apiClient, "PUT");
		remote.GET.mockResolvedValue(policy("off"));
		remote.PUT.mockResolvedValue(policy("risky"));
		render(
			<DropdownMenu open>
				<DropdownMenuTrigger>menu</DropdownMenuTrigger>
				<DropdownMenuContent>
					<ApprovalModeMenuItems sessionId="ao-9" hostId="box" onBack={vi.fn()} />
				</DropdownMenuContent>
			</DropdownMenu>,
		);
		expect(remote.GET).toHaveBeenCalledWith("/api/v1/approvals/policy", { params: { query: { sessionId: "ao-9" } } });
		await waitFor(() => expect(screen.getByRole("menuitem", { name: "Risky actions" })).not.toHaveAttribute("data-disabled"));
		fireEvent.click(screen.getByRole("menuitem", { name: "Risky actions" }));
		await waitFor(() => expect(remote.PUT).toHaveBeenCalledWith("/api/v1/approvals/policy", { body: { sessionId: "ao-9", mode: "risky" } }));
		expect(localGet).not.toHaveBeenCalled();
		expect(localPut).not.toHaveBeenCalled();
	});

	it("scopes the policy cache by host", () => {
		expect(approvalPolicyQueryKey("ao-9", "box")).not.toEqual(approvalPolicyQueryKey("ao-9"));
	});

	it("fits the root row in the menu: the mode gives way first, then the label ellipsizes", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue(policy("every-submit") as never);
		render(
			<DropdownMenu open>
				<DropdownMenuTrigger>menu</DropdownMenuTrigger>
				<DropdownMenuContent>
					<ApprovalModeMenuRow sessionId="ao-7" onOpen={vi.fn()} />
				</DropdownMenuContent>
			</DropdownMenu>,
		);
		const value = await screen.findByText("Every form submission");
		const label = screen.getByText("Ask before acting");
		// Label and mode share one box that takes the space between the icon
		// and the chevron and clips anything past it, so nothing overflows the menu.
		const box = label.parentElement!;
		expect(value.parentElement).toBe(box);
		expect(box).toHaveClass("flex", "min-w-0", "flex-1", "overflow-hidden");
		// The label keeps its full width when it fits and never grows past the
		// box; a longer translation ellipsizes and stays readable on hover.
		expect(label).toHaveClass("shrink-0", "max-w-full", "truncate");
		expect(label).toHaveAttribute("title", "Ask before acting");
		// The mode takes what is left.
		expect(value).toHaveClass("min-w-0", "max-w-20", "truncate");
		expect(value).toHaveAttribute("title", "Every form submission");
	});
});
