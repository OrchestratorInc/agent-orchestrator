import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MoveError } from "../lib/move-session";
import { useMoveStore, type MoveState } from "../lib/move-runner";
import { MoveBanner } from "./MoveBanner";

const actions = vi.hoisted(() => ({
	key: "local:web-3",
	start: vi.fn(),
	choose: vi.fn(),
	retry: vi.fn(),
	retryCommit: vi.fn(async () => {}),
	keepHere: vi.fn(async (_transferId?: string) => {}),
	dismiss: vi.fn(),
}));
vi.mock("../hooks/useMoveSession", () => ({ useMoveSession: () => actions }));

const MB = 1024 * 1024;
const session = { id: "web-3", title: "Osprey notes", workspaceId: "web" } as never;
const base = { targetHostId: "host-b", targetProjectId: "web", targetLabel: "MacBook B", title: "Osprey notes" };
const setMove = (move: Partial<MoveState>) => useMoveStore.setState({ moves: { "local:web-3": { ...base, phase: "moving", ...move } as MoveState }, shown: {} });

describe("MoveBanner", () => {
	afterEach(() => {
		useMoveStore.setState({ moves: {}, shown: {} });
		vi.clearAllMocks();
	});

	it("asks when to move a session that is mid-turn", () => {
		setMove({ phase: "asking" });
		render(<MoveBanner session={session} />);
		expect(screen.getByText("Claude is working on a turn")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Move after this turn" }));
		expect(actions.retry).toHaveBeenCalledWith({ afterTurn: true });
		fireEvent.click(screen.getByRole("button", { name: "Stop turn and move" }));
		expect(actions.retry).toHaveBeenCalledWith({ stopTurn: true });
	});

	it("shows the copy's real progress and counts itself as shown", () => {
		setMove({ step: "copy", progress: { sent: 12 * MB, total: 32 * MB } });
		const view = render(<MoveBanner session={session} />);
		expect(screen.getByText("Moving to MacBook B")).toBeInTheDocument();
		expect(screen.getByText(/Copying the conversation · 12 of 32 MB/)).toBeInTheDocument();
		expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "38");
		expect(useMoveStore.getState().shown["local:web-3"]).toBe(1);
		view.unmount();
		expect(useMoveStore.getState().shown["local:web-3"]).toBe(0);
	});

	it("offers Keep it here and Retry when the destination stopped answering", () => {
		setMove({ phase: "failed", error: new MoveError("MacBook B is not reachable", "receive", false, "RECEIVE_UNCONFIRMED", "tr1") });
		render(<MoveBanner session={session} />);
		expect(screen.getByRole("alert")).toHaveTextContent("MacBook B stopped answering");
		fireEvent.click(screen.getByRole("button", { name: "Keep it here" }));
		expect(actions.keepHere).toHaveBeenCalledWith("tr1");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(actions.retry).toHaveBeenCalled();
	});

	it("offers no Retry for a refusal the same move cannot get past", () => {
		setMove({ phase: "failed", error: new MoveError("the session is on the default branch", "prepare", true, "TRANSFER_DEFAULT_BRANCH") });
		render(<MoveBanner session={session} />);
		expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Close" }));
		expect(actions.dismiss).toHaveBeenCalled();
	});
});
