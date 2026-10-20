import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AwakeState } from "../hooks/useAwake";
import { useUiStore } from "../stores/ui-store";
import { AwakeControl } from "./AwakeControl";
import { TooltipProvider } from "./ui/tooltip";
const test = vi.hoisted(() => ({
	act: vi.fn(),
	mac: true,
	state: undefined as AwakeState | undefined,
}));
vi.mock("../lib/platform", () => ({ isMacPlatform: () => test.mac }));
vi.mock("../hooks/useAwake", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useAwake")>()),
	useAwake: () => ({
		state: test.state,
		act: test.act,
		busy: false,
		error: null,
	}),
}));
beforeEach(() => {
	test.act.mockReset();
	test.mac = true;
	test.state = {
		settings: { lidClosed: true },
		sessions: [],
		capabilities: [{ id: "idle", available: true }],
	} as unknown as AwakeState;
});
function show(sessionId?: string) {
	return render(
		<TooltipProvider>
			<AwakeControl sessionId={sessionId} compact={!sessionId} />
		</TooltipProvider>,
	);
}
describe("Stay Awake quick controls", () => {
	it("starts an indefinite request scoped to this session", () => {
		show("work");
		fireEvent.click(screen.getByRole("switch", { name: "Stay Awake" }));
		expect(test.act).toHaveBeenCalledWith({
			kind: "start",
			request: {
				scope: "session",
				sessionId: "work",
				mode: "indefinite",
				lidClosed: true,
			},
		});
	});
	it("toolbar starts an independent AO request without mobile access", () => {
		show();
		fireEvent.click(screen.getByRole("button", { name: "Stay Awake" }));
		expect(test.act).toHaveBeenCalledWith({
			kind: "start",
			request: {
				scope: "ao",
				sessionId: "",
				mode: "indefinite",
				lidClosed: true,
			},
		});
	});
	it("ends only requests owned by the selected scope", () => {
		test.state!.sessions = [
			{ id: "mine", scope: "session", sessionId: "work" },
			{ id: "other", scope: "session", sessionId: "other" },
			{ id: "global", scope: "ao" },
		] as AwakeState["sessions"];
		show("work");
		fireEvent.click(screen.getByRole("switch", { name: "Stay Awake" }));
		expect(test.act).toHaveBeenCalledTimes(1);
		expect(test.act).toHaveBeenCalledWith({ kind: "end", id: "mine" });
	});
	it("info opens the Stay Awake settings page", () => {
		show();
		fireEvent.click(
			screen.getByRole("button", { name: "Stay Awake settings" }),
		);
		expect(useUiStore.getState().settingsModal).toMatchObject({
			scope: "global",
			section: "stayAwake",
		});
	});
	it("does not offer control before capability discovery or on another OS", () => {
		test.state = undefined;
		const view = show();
		expect(screen.getByRole("button", { name: "Stay Awake" })).toBeDisabled();
		view.unmount();
		test.mac = false;
		show();
		expect(screen.queryByTestId("awake-toolbar")).toBeNull();
	});
});
