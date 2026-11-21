import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PendingApproval } from "../../lib/approvals";
import { ApprovalPrompt, approvalTimeLeft } from "./ApprovalPrompt";

const request: PendingApproval = {
	id: "apr_1",
	kind: "browser-action",
	sessionId: "ao-7",
	site: "shop.example",
	url: "https://shop.example/checkout",
	pageTitle: "Checkout",
	action: 'Click "Place order" on shop.example',
	category: "payment",
	reason: 'The button "Place order" reads like a payment or purchase.',
	element: 'button "Place order"',
	image: "iVBORw0KGgo=",
	createdAt: new Date(Date.now()).toISOString(),
	expiresAt: new Date(Date.now() + 65_000).toISOString(),
};

describe("ApprovalPrompt", () => {
	afterEach(() => vi.useRealTimers());

	it("says what will happen, where, and why, with the element", () => {
		render(<ApprovalPrompt request={request} onDecide={vi.fn()} />);
		expect(screen.getByRole("alertdialog", { name: "Agent action waiting for approval" })).toBeInTheDocument();
		expect(screen.getByTestId("approval-action")).toHaveTextContent('Click "Place order" on shop.example');
		expect(screen.getByText(/reads like a payment/)).toBeInTheDocument();
		expect(screen.getByText(/Approval needed · Payment · shop\.example/)).toBeInTheDocument();
		expect(screen.getByTestId("approval-image")).toHaveAttribute("src", "data:image/png;base64,iVBORw0KGgo=");
		expect(screen.getByTestId("approval-image")).toHaveAttribute("alt", 'The element the agent wants to use: button "Place order"');
		expect(screen.getByText(/Checkout — https:\/\/shop\.example\/checkout/)).toBeInTheDocument();
		expect(screen.getByTestId("approval-countdown")).toHaveTextContent(/Denies in 1:0[45]/);
	});

	it("sends each decision", () => {
		const onDecide = vi.fn();
		render(<ApprovalPrompt request={request} onDecide={onDecide} />);
		fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
		fireEvent.click(screen.getByRole("button", { name: "Always allow on shop.example" }));
		fireEvent.click(screen.getByRole("button", { name: /Deny/ }));
		expect(onDecide.mock.calls.map(([decision]) => decision)).toEqual(["allow-once", "allow-site", "deny"]);
	});

	it("only says who it waits for when someone else decides it", () => {
		const onDecide = vi.fn();
		render(<ApprovalPrompt request={{ ...request, waitingFor: { name: "Priya Shah" } }} onDecide={onDecide} />);
		expect(screen.getByRole("status", { name: "Agent action waiting for approval" })).toBeInTheDocument();
		expect(screen.queryByRole("alertdialog")).toBeNull();
		expect(screen.getByTestId("approval-waiting")).toHaveTextContent("Waiting for Priya Shah to approve");
		expect(screen.queryByRole("button", { name: /Allow once|Always allow|Deny/ })).toBeNull();
		expect(screen.getByTestId("approval-action")).toHaveTextContent('Click "Place order" on shop.example');
		const prompt = screen.getByTestId("approval-prompt");
		expect(prompt).not.toHaveFocus();
		fireEvent.keyDown(prompt, { key: "Escape" });
		expect(onDecide).not.toHaveBeenCalled();
	});

	it("names the session owner when the decider has no name", () => {
		render(<ApprovalPrompt request={{ ...request, waitingFor: {} }} onDecide={vi.fn()} />);
		expect(screen.getByTestId("approval-waiting")).toHaveTextContent("Waiting for the session owner to approve");
	});

	it("denies on Escape and never approves on Enter", () => {
		const onDecide = vi.fn();
		render(<ApprovalPrompt request={request} onDecide={onDecide} />);
		const prompt = screen.getByTestId("approval-prompt");
		expect(prompt).toHaveFocus();
		fireEvent.keyDown(prompt, { key: "Enter" });
		expect(onDecide).not.toHaveBeenCalled();
		fireEvent.keyDown(prompt, { key: "Escape" });
		expect(onDecide).toHaveBeenCalledWith("deny");
	});

	it("does not approve when Enter is pressed on a focused approval button", () => {
		const onDecide = vi.fn();
		render(<ApprovalPrompt request={request} onDecide={onDecide} />);
		for (const name of ["Allow once", "Always allow on shop.example"]) {
			const button = screen.getByRole("button", { name });
			button.focus();
			// fireEvent returns false when the default (the native click) was prevented.
			expect(fireEvent.keyDown(button, { key: "Enter" })).toBe(false);
		}
		expect(onDecide).not.toHaveBeenCalled();
	});

	it("disables decisions while one is being sent and shows a failure in place", () => {
		const onDecide = vi.fn();
		render(<ApprovalPrompt request={request} onDecide={onDecide} busy error="daemon offline" />);
		expect(screen.getByRole("button", { name: "Allow once" })).toBeDisabled();
		fireEvent.keyDown(screen.getByTestId("approval-prompt"), { key: "Escape" });
		expect(onDecide).not.toHaveBeenCalled();
		expect(screen.getByRole("alert")).toHaveTextContent("The decision could not be sent: daemon offline");
	});

	it("labels a saved login's first use and names the session when shown elsewhere", () => {
		const onOpenSession = vi.fn();
		render(
			<ApprovalPrompt
				request={{ ...request, kind: "credential-use", category: "credential", image: undefined }}
				onDecide={vi.fn()}
				sessionLabel="Fix checkout bug"
				onOpenSession={onOpenSession}
				more={2}
			/>,
		);
		expect(screen.getByText(/Saved login · shop\.example · Fix checkout bug/)).toBeInTheDocument();
		expect(screen.queryByText(/Saved login · Saved login/)).not.toBeInTheDocument();
		expect(screen.queryByTestId("approval-image")).not.toBeInTheDocument();
		expect(screen.getByText("+2 more")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(onOpenSession).toHaveBeenCalled();
	});

	it("offers no Always allow for the catch-all script category", () => {
		// "script" covers eval, unknown commands and unreadable targets: a
		// site rule for it would let any later script run unasked.
		render(<ApprovalPrompt request={{ ...request, category: "script" }} onDecide={vi.fn()} />);
		expect(screen.queryByRole("button", { name: /Always allow/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Allow once" })).toBeInTheDocument();
	});

	it("offers no site-wide allow when the site is unknown", () => {
		render(<ApprovalPrompt request={{ ...request, site: "" }} onDecide={vi.fn()} />);
		expect(screen.queryByRole("button", { name: /Always allow/ })).not.toBeInTheDocument();
	});
});

describe("approvalTimeLeft", () => {
	it("counts down to zero", () => {
		expect(approvalTimeLeft("2026-01-01T00:01:30Z", Date.parse("2026-01-01T00:00:00Z"))).toBe("1:30");
		expect(approvalTimeLeft("2026-01-01T00:00:00Z", Date.parse("2026-01-01T00:00:05Z"))).toBe("0:00");
	});
});
