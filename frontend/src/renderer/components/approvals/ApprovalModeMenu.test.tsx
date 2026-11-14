import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { ApprovalModeMenuItems, ApprovalModeMenuRow } from "./ApprovalModeMenu";

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
});
