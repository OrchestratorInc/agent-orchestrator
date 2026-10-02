import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { typeInLexicalEditor } from "../test/lexical";
import { TooltipProvider } from "./ui/tooltip";
import { CloudSessionComposer } from "./CloudSessionComposer";

const sendSessionMessage = vi.fn();
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ baseUrl: "https://cloud.test", client: { sendSessionMessage }, ready: true }),
}));

function renderComposer(props: Partial<Parameters<typeof CloudSessionComposer>[0]> = {}) {
	render(
		<CloudSessionComposer agentWorking={false} autoFocus={false} disabled={false} orgId="org-1" sessionId="session-1" {...props} />,
		{ wrapper: TooltipProvider },
	);
	return screen.getByLabelText("Message the agent") as HTMLElement;
}

describe("CloudSessionComposer", () => {
	beforeEach(() => {
		sendSessionMessage.mockReset().mockResolvedValue({ event: {} });
	});

	it("sends the whole message to the cloud session on Enter", async () => {
		const field = renderComposer();
		await typeInLexicalEditor(field, "run the tests");
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(sendSessionMessage).toHaveBeenCalledWith("org-1", "session-1", { text: "run the tests" }));
	});

	it("says a message waits while the agent is working", () => {
		renderComposer({ agentWorking: true });
		expect(screen.getByText("Agent is working — this sends when it finishes")).toBeInTheDocument();
	});
});
