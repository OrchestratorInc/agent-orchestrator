import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReviewerChatSurface } from "./ReviewerChatSurface";

const mocks = vi.hoisted(() => ({ openSessionLink: vi.fn() }));

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../../hooks/useReviewerConversation", () => ({
	useReviewerConversation: () => ({
		snapshot: { sessionId: "review-session" },
		isLoading: false,
		error: undefined,
		hasOlder: false,
		isLoadingOlder: false,
		loadOlder: vi.fn(),
	}),
	useReviewerConversationCommands: () => ({
		busy: false,
		error: undefined,
		send: vi.fn(),
		resolve: vi.fn(),
		resolveInput: vi.fn(),
		interrupt: vi.fn(),
	}),
}));
vi.mock("../../lib/use-session-link-navigation", () => ({
	useSessionLinkNavigation: () => mocks.openSessionLink,
}));
vi.mock("./ChatWorkspace", () => ({
	ChatWorkspace: ({ onSessionLinkOpen, draftOwner }: { onSessionLinkOpen?: (url: string) => void; draftOwner?: { sessionId: string } }) => (
		<button type="button" data-draft-owner={draftOwner?.sessionId} onClick={() => onSessionLinkOpen?.("ao://sessions/project/session")}>
			Open session
		</button>
	),
}));

describe("ReviewerChatSurface", () => {
	beforeEach(() => mocks.openSessionLink.mockReset());

	it("routes session links through in-app navigation", () => {
		render(<ReviewerChatSurface reviewId="review-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(mocks.openSessionLink).toHaveBeenCalledWith("ao://sessions/project/session");
	});

	it("keeps reviewer drafts isolated across remote hosts", () => {
		render(<ReviewerChatSurface reviewId="review-1" hostId="https://remote.example" />);
		expect(screen.getByRole("button", { name: "Open session" })).toHaveAttribute(
		"data-draft-owner",
		"remote:https%3A%2F%2Fremote.example:review%3Areview-1",
	);
	});
});
