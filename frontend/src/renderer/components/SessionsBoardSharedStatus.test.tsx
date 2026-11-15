import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BoardSessionPresentation } from "@aoagents/product-ui";
import { appI18n } from "../i18n";
import type { WorkspaceSession } from "../types/workspace";
import { BoardSessionCardAdapter, withSharedWorkStatus } from "./SessionsBoardAdapters";
import { TooltipProvider } from "./ui/tooltip";

// The card reads its PRs from the daemon; these cards have none.
vi.mock("../hooks/useSessionScmSummary", () => ({ useSessionScmSummary: () => ({ data: undefined }) }));

const card: BoardSessionPresentation = { id: "s1", provider: "codex", status: "review_pending", title: "Login flow", updatedAt: "2026-10-24T00:00:00Z" };
const t = appI18n.t.bind(appI18n);

describe("withSharedWorkStatus", () => {
	it("says who else is writing to the agent, in the working color", () => {
		const shown = withSharedWorkStatus(card, [
			{ userId: "me", name: "Vaibhaav", present: true, state: "idle" },
			{ userId: "v", name: "Vaanya", present: true, state: "working" },
		], "me", t);
		expect(shown.statusPresentation).toMatchObject({ label: "Vaanya working", className: "text-status-working" });
	});

	it("leaves the card alone while nobody else is writing, or when it is the person themselves", () => {
		expect(withSharedWorkStatus(card, [{ userId: "v", name: "Vaanya", present: true, state: "idle" }], "me", t)).toBe(card);
		expect(withSharedWorkStatus(card, [{ userId: "me", name: "Me", present: true, state: "working" }], "me", t)).toBe(card);
		expect(withSharedWorkStatus(card, undefined, "me", t)).toBe(card);
	});

	it("keeps a provisioning card's own line", () => {
		const provisioning = { ...card, statusPresentation: { className: "x", indicatorClassName: "y", label: "Starting" } };
		expect(withSharedWorkStatus(provisioning, [{ userId: "v", name: "Vaanya", present: true, state: "working" }], "me", t)).toBe(provisioning);
	});
});

describe("sharing from a board card", () => {
	afterEach(() => cleanup());

	const session: WorkspaceSession = {
		id: "s1", workspaceId: "p1", workspaceName: "acme/web", title: "Login flow", provider: "codex", status: "working",
		kanbanColumn: "building", updatedAt: "2026-10-24T00:00:00Z", prs: [], cloud: { orgId: "org" },
	};
	function renderCard(props: Partial<React.ComponentProps<typeof BoardSessionCardAdapter>> = {}) {
		const onOpen = vi.fn();
		const onShare = vi.fn();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<BoardSessionCardAdapter onOpen={onOpen} onShare={onShare} session={session} {...props} />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		return { ...view, onOpen, onShare };
	}

	it("opens the share dialog from a Share button, without opening the session", () => {
		const { onOpen, onShare } = renderCard();
		const share = screen.getByRole("button", { name: "Share “Login flow”" });
		// Revealed on hover or focus, as the card's Archive is.
		expect(share.className).toContain("group-hover:opacity-100");
		expect(share.className).toContain("group-focus-within:opacity-100");
		fireEvent.click(share);
		expect(onShare).toHaveBeenCalledTimes(1);
		expect(onOpen).not.toHaveBeenCalled();
	});

	it("shows no Share button where this person can't share the session", () => {
		renderCard({ onShare: undefined });
		expect(screen.queryByRole("button", { name: "Share “Login flow”" })).toBeNull();
	});

	it("opens the same dialog from the people circles of a shared session, without opening the session", () => {
		const { container, onOpen, onShare } = renderCard({
			me: "me",
			participants: [{ userId: "v", name: "Vaanya", present: true, state: "idle" }],
		});
		const circles = container.querySelector<HTMLElement>("[data-session-people]")!;
		expect(circles.tagName).toBe("BUTTON");
		fireEvent.click(circles);
		expect(onShare).toHaveBeenCalledTimes(1);
		expect(onOpen).not.toHaveBeenCalled();
	});

	it("keeps the circles a plain group where this person can't share the session", () => {
		const { container, onOpen } = renderCard({
			onShare: undefined,
			participants: [{ userId: "v", name: "Vaanya", present: true, state: "idle" }],
		});
		const circles = container.querySelector<HTMLElement>("[data-session-people]")!;
		expect(circles.tagName).toBe("SPAN");
		expect(onOpen).not.toHaveBeenCalled();
	});
});
