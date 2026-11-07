import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { CloudSessionNotificationReader } from "./CloudSessionNotificationReader";

const { readSessionMock } = vi.hoisted(() => ({ readSessionMock: vi.fn() }));

vi.mock("../hooks/useCloudNotifications", () => ({
	useReadCloudSessionNotifications: (sessionId: string | undefined, enabled: boolean) => readSessionMock(sessionId, enabled),
}));

describe("CloudSessionNotificationReader", () => {
	afterEach(() => {
		useUiStore.setState({ settingsModal: null });
	});

	it("reads the session's notifications while it is on screen, not while Settings covers it", () => {
		const { rerender } = render(<CloudSessionNotificationReader sessionId="session-1" />);
		expect(readSessionMock).toHaveBeenLastCalledWith("session-1", true);

		useUiStore.setState({ settingsModal: { scope: "global" } });
		rerender(<CloudSessionNotificationReader sessionId="session-1" />);
		expect(readSessionMock).toHaveBeenLastCalledWith("session-1", false);
	});
});
