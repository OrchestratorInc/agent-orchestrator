import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { DeliveryCardView, type DeliveryStatus } from "./SessionDeliveryCard";

const delivery = (overrides: Partial<DeliveryStatus> = {}): DeliveryStatus => ({ state: "ready_to_publish", action: "publish_pr", workspaceVersion: "v1", branch: "ao/card", repository: "acme/widget", commitCount: 1, commitSubject: "feat: delivery card", changedFiles: 3, additions: 20, deletions: 4, ...overrides });

describe("SessionDeliveryCard", () => {
	it("shows committed change facts and creates a pull request", async () => {
		const onAdvance = vi.fn();
		render(<DeliveryCardView delivery={delivery()} onAdvance={onAdvance} />);
		expect(screen.getByText("feat: delivery card")).toBeInTheDocument();
		expect(screen.getByText(/3 files/)).toHaveTextContent("+20");
		await userEvent.click(screen.getByRole("button", { name: "Create pull request" }));
		expect(onAdvance).toHaveBeenCalledWith();
	});

	it("requires an editable non-empty message before commit and push", async () => {
		const onAdvance = vi.fn();
		render(<DeliveryCardView delivery={delivery({ state: "uncommitted_for_pr", action: "commit_and_push", pullRequest: { number: 42, url: "https://example/pr/42" }, commitCount: 0, commitSubject: undefined })} onAdvance={onAdvance} />);
		await userEvent.click(screen.getByRole("button", { name: "Commit & push to PR #42" }));
		const input = screen.getByLabelText("Commit message");
		expect(input).toHaveFocus();
		await userEvent.clear(input);
		expect(screen.getByRole("button", { name: "Commit & push to PR #42" })).toBeDisabled();
		await userEvent.type(input, "fix: follow-up");
		await userEvent.click(screen.getByRole("button", { name: "Commit & push to PR #42" }));
		expect(onAdvance).toHaveBeenCalledWith("fix: follow-up");
	});

	it("does not offer a mutation for empty, synchronized, or blocked states", () => {
		const onAdvance = vi.fn();
		const { rerender } = render(<DeliveryCardView delivery={delivery({ state: "empty", action: undefined, commitCount: 0, commitSubject: undefined, changedFiles: 0, additions: 0, deletions: 0 })} onAdvance={onAdvance} />);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		rerender(<DeliveryCardView delivery={delivery({ state: "blocked", action: undefined, blockedReason: "Branch diverged" })} onAdvance={onAdvance} />);
		expect(screen.getByText("Branch diverged")).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		rerender(<DeliveryCardView delivery={delivery({ state: "synchronized", action: undefined })} onAdvance={onAdvance} />);
		expect(screen.queryByLabelText("Session delivery")).not.toBeInTheDocument();
	});

	it("prevents duplicate submission while pending and announces progress", () => {
		render(<DeliveryCardView delivery={delivery()} onAdvance={vi.fn()} pending />);
		expect(screen.getByRole("button", { name: "Create pull request" })).toBeDisabled();
		expect(screen.getByText("Delivery action in progress")).toBeInTheDocument();
	});
});
