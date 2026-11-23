import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { ChatConnectorsMenu, connectorRowState } from "./ChatConnectorsMenu";

function renderMenu(servers: Parameters<typeof ChatConnectorsMenu>[0]["servers"] = []) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ChatConnectorsMenu sessionId="mer-1" servers={servers} />
		</QueryClientProvider>,
	);
}

const three = {
	data: {
		connectors: [
			{ key: "linear", name: "Linear", transport: "http", status: "connected", enabled: true, calls: 4, lastTool: "create_issue" },
			{ key: "sentry", name: "Sentry", transport: "http", status: "needs_signin", enabled: true, calls: 0 },
			{ key: "files", name: "files", transport: "stdio", status: "connected", enabled: true, calls: 0 },
		],
	},
};

describe("ChatConnectorsMenu", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		useUiStore.setState({ settingsModal: null });
	});

	it("shows nothing for a chat without connectors", async () => {
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: [] } } as never);
		const { container } = renderMenu();
		await vi.waitFor(() => expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/connectors", { params: { path: { sessionId: "mer-1" } } }));
		expect(container).toBeEmptyDOMElement();
	});

	it("lists each connector with its calls and its state, and opens Settings", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue(three as never);
		const user = userEvent.setup();
		renderMenu([{ name: "linear", status: "ready" }, { name: "files", status: "failed", error: "spawn npx ENOENT" }, { name: "ao", status: "ready" }]);
		const chip = await screen.findByRole("button", { name: "3 connectors in this chat" });
		expect(chip).toHaveTextContent("3");
		await user.click(chip);
		const rows = screen.getAllByRole("listitem");
		expect(rows).toHaveLength(3);
		expect(rows[0]).toHaveTextContent("Linear4 calls · create_issueLoaded");
		expect(rows[1]).toHaveTextContent("SentryNo calls yetNeeds sign-in");
		// Calls to a local command are not counted, so the row does not say "No calls yet".
		expect(rows[2]).toHaveTextContent("filesLocal commandFailed");
		expect(screen.getByText("Failed")).toHaveAttribute("title", "spawn npx ENOENT");
		await user.click(screen.getByRole("menuitem", { name: "Manage connectors" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "connectors" });
	});

	it("does not claim an agent loaded a server it has not reported", () => {
		const on = { enabled: true, status: "connected" as const };
		expect(connectorRowState(on)).toBe("added");
		expect(connectorRowState(on, { status: "ready" })).toBe("loaded");
		expect(connectorRowState(on, { status: "starting" })).toBe("starting");
		expect(connectorRowState(on, { status: "cancelled" })).toBe("failed");
		expect(connectorRowState({ enabled: true, status: "needs_signin" }, { status: "ready" })).toBe("needsSignIn");
		expect(connectorRowState({ enabled: false, status: "connected" }, { status: "ready" })).toBe("off");
	});
});
