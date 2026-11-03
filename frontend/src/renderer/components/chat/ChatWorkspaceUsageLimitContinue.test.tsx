/**
 * A chat that hit its account's usage limit, while another signed-in account
 * still has quota. Moving one session does not move the others, so each
 * limited chat offers the account that can still answer: one click moves the
 * session there and sends the failed prompt again once the move completes.
 */

import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";
import { renderChat as render } from "../../test/render-chat";

const lab = vi.hoisted(() => ({
	switchSession: vi.fn(),
	otherUsedPercent: 22,
	transition: undefined as undefined | { id: string; phase: string; errorDetail?: string },
}));

vi.mock("../../hooks/useAgentAccounts", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useAgentAccounts")>()),
	useSessionAgentAccounts: (sessionId: string | undefined) => ({ data: sessionId ? { codex: "work" } : undefined }),
	useAgentAccounts: (enabled = true) => ({
		data: enabled ? [
			{
				id: "work", provider: "codex", device: false, default: true, email: "work@example.com", signedIn: true,
				limits: { fetchedAt: "2026-10-26T10:00:00Z", windows: [{ id: "primary", label: "5h", usedPercent: 100 }] },
			},
			{
				id: "device-codex", provider: "codex", device: true, default: false, email: "me@example.com", signedIn: true,
				limits: { fetchedAt: "2026-10-26T10:00:00Z", windows: [{ id: "primary", label: "5h", usedPercent: lab.otherUsedPercent }] },
			},
		] : undefined,
	}),
	useAgentAccountActions: () => ({ switchSession: lab.switchSession }),
}));

vi.mock("../../hooks/useSessionInterfaceTransition", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useSessionInterfaceTransition")>()),
	useSessionInterfaceTransitionStatus: () => ({ transition: lab.transition, status: undefined, isLoading: false }),
}));

afterEach(() => {
	lab.switchSession.mockReset();
	lab.otherUsedPercent = 22;
	lab.transition = undefined;
});

function limitedSnapshot(): ConversationSnapshot {
	return {
		...chatFixture,
		controller: { state: "ready" },
		turns: chatFixture.turns.filter((turn) => turn.id === "turn-1").map((turn) => ({
			...turn,
			state: "failed" as const,
			completedAt: turn.requestedAt,
			errorMessage: "You've hit your session limit",
			hasHumanPrompt: true,
			usageLimit: { resetsAt: new Date(Date.now() + 3_600_000).toISOString(), autoResume: false },
		})),
		items: chatFixture.items.filter((item) => item.turnId === "turn-1"),
	};
}

describe("ChatWorkspace usage limit on one account", () => {
	it("continues on another account with quota and sends the prompt again once the chat moved", async () => {
		lab.switchSession.mockResolvedValue({ id: "move-1", phase: "requested" });
		const retry = vi.fn();
		const view = render(<ChatWorkspace snapshot={limitedSnapshot()} retryControl={{ retry, setAutoResume: vi.fn() }} />);

		await userEvent.click(screen.getByRole("button", { name: "Continue on me@example.com" }));
		expect(lab.switchSession).toHaveBeenCalledWith(chatFixture.sessionId, "device-codex");
		expect(retry).not.toHaveBeenCalled();

		lab.transition = { id: "move-1", phase: "completed" };
		view.rerender(<ChatWorkspace snapshot={limitedSnapshot()} retryControl={{ retry, setAutoResume: vi.fn() }} />);
		await waitFor(() => expect(retry).toHaveBeenCalledWith("turn-1"));
		expect(retry).toHaveBeenCalledOnce();
	});

	it("does not offer an account that has run out too", () => {
		lab.otherUsedPercent = 100;
		render(<ChatWorkspace snapshot={limitedSnapshot()} retryControl={{ retry: vi.fn(), setAutoResume: vi.fn() }} />);
		expect(screen.getByRole("button", { name: "Auto-resume at reset" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Continue on/ })).not.toBeInTheDocument();
	});
});
