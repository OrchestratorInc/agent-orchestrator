import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatModel, TurnSettings } from "../../types/conversation";
import { ReviewerChatSurface } from "./ReviewerChatSurface";

const mocks = vi.hoisted(() => ({ openSessionLink: vi.fn(), chooseSettings: vi.fn(), resumeAgent: vi.fn() }));

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../../hooks/useReviewerConversation", () => ({
	useReviewerConversation: () => ({
		snapshot: { sessionId: "review-session", controller: { state: "ready" } },
		isLoading: false,
		error: undefined,
		hasOlder: false,
		isLoadingOlder: false,
		loadOlder: vi.fn(),
	}),
	useReviewerConversationModels: () => ({ models: [{ id: "review-model", displayName: "Review model", efforts: ["high"] }], error: undefined }),
	useReviewerConversationCommands: () => ({
		chooseSettings: mocks.chooseSettings,
		resumeAgent: mocks.resumeAgent,
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
	ChatWorkspace: ({ onResumeAgent, onSessionLinkOpen, draftOwner, models, onChooseSettings, approvalModes }: { onResumeAgent?: () => void; onSessionLinkOpen?: (url: string) => void; draftOwner?: { sessionId: string }; models?: ChatModel[]; onChooseSettings?: (settings: TurnSettings) => void; approvalModes?: string[] }) => (
		<>
		<button onClick={onResumeAgent}>Resume agent</button>
		<button type="button" data-draft-owner={draftOwner?.sessionId} onClick={() => onSessionLinkOpen?.("ao://sessions/project/session")}>
			Open session
		</button>
		<button type="button" data-approval-modes={approvalModes?.join(",")} onClick={() => onChooseSettings?.({ model: models?.[0]?.id, reasoningEffort: "high", approvalMode: "auto" })}>Choose reviewer settings</button>
		</>
	),
}));

describe("ReviewerChatSurface", () => {
	beforeEach(() => { mocks.openSessionLink.mockReset(); mocks.chooseSettings.mockReset(); });

	it("connects the shared Resume control to reviewer recovery for its worker owner", () => {
		render(<ReviewerChatSurface workerSessionId="worker-1" reviewId="review-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Resume agent" }));
		expect(mocks.resumeAgent).toHaveBeenCalledWith("worker-1");
	});

	it("routes session links through in-app navigation", () => {
		render(<ReviewerChatSurface workerSessionId="worker-1" reviewId="review-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(mocks.openSessionLink).toHaveBeenCalledWith("ao://sessions/project/session");
	});

	it("exposes reviewer models and effort while keeping unattended permissions fixed", () => {
		render(<ReviewerChatSurface workerSessionId="worker-1" reviewId="review-1" />);
		const choose = screen.getByRole("button", { name: "Choose reviewer settings" });
		expect(choose).toHaveAttribute("data-approval-modes", "auto");
		fireEvent.click(choose);
		expect(mocks.chooseSettings).toHaveBeenCalledWith({ model: "review-model", reasoningEffort: "high", approvalMode: "auto" });
	});

	it("keeps reviewer drafts isolated across remote hosts", () => {
		render(<ReviewerChatSurface workerSessionId="worker-1" reviewId="review-1" hostId="https://remote.example" />);
		expect(screen.getByRole("button", { name: "Open session" })).toHaveAttribute(
		"data-draft-owner",
		"remote:https%3A%2F%2Fremote.example:review%3Areview-1",
	);
	});
});
