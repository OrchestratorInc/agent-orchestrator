import { render, renderHook, act, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: post }) }));
import { ProviderSwitchFailed, useConversationCommands, useProviderSwitchCommand } from "./useConversation";
import { ChatComposer, AGENT_SWITCH_FAILED } from "../components/chat/ChatComposer";
import { TooltipProvider } from "../components/ui/tooltip";
import { typeInLexicalEditor } from "../test/lexical";
import { readChatSessionDraft } from "../lib/chat-drafts";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}
const unavailable = {
	code: "PROVIDER_SWITCH_unavailable",
	message: "Pi chat adapter is not installed",
};

describe("a definite daemon refusal versus gateway uncertainty", () => {
	it("leaves the real composer editable after a definitive daemon preflight refusal", async () => {
		post.mockResolvedValue({ data: undefined, error: unavailable, response: new Response(null, {status:503}) });
		const scope = "adversarial-provider-unavailable-503";
		let settled = false;
		function Compose() {
			const commands = useConversationCommands("ao-1");
			return <TooltipProvider><ChatComposer draftSessionId={scope} onSend={async (text, attachments, clientMessageId) => {
				// The catch below is the SessionChatSurface send path: definitive switch
				// failures acquire AGENT_SWITCH_FAILED, other failures retain their identity.
				try {
					return await commands.send({text,attachments,clientMessageId,providerSwitch:{targetHarness:"pi"}});
				} catch (failure) {
					if (failure instanceof ProviderSwitchFailed) throw Object.assign(new Error(failure.message), {code:AGENT_SWITCH_FAILED});
					throw failure;
				} finally {
					settled = true;
				}
			}} /></TooltipProvider>;
		}
		render(<Compose />, {wrapper});
		const field = screen.getByLabelText("Message the agent");
		await typeInLexicalEditor(field, "Keep this editable");
		fireEvent.keyDown(field, {key:"Enter"});
		await waitFor(() => expect(post).toHaveBeenCalled());
		await waitFor(() => expect(settled).toBe(true));
		expect(field).toHaveAttribute("contenteditable", "true");
		expect(readChatSessionDraft(scope).composer.delivery).toBeUndefined();
	});
	it("returns an editable-draft switch refusal for the daemon's explicit 503 unavailable response", async () => {
		post.mockResolvedValue({ data: undefined, error: unavailable, response: new Response(null, {status:503}) });
		const { result } = renderHook(() => useConversationCommands("ao-1"), {wrapper});
		let failure: unknown;
		await act(async () => {
			failure = await result.current.send({text:"Keep this editable",clientMessageId:"m1",providerSwitch:{targetHarness:"pi"}}).catch(e => e);
		});
		expect(failure).toBeInstanceOf(ProviderSwitchFailed);
	});
	it("still treats a gateway 503 with no definitive daemon refusal as uncertain", async () => {
		const unknown = {code:"UPSTREAM_UNAVAILABLE",message:"Gateway lost connection"};
		post.mockResolvedValue({ data: undefined, error: unknown, response: new Response(null, {status:503}) });
		const { result } = renderHook(() => useConversationCommands("ao-1"), {wrapper});
		let failure: unknown;
		await act(async () => {
			failure = await result.current.send({text:"Do not duplicate",clientMessageId:"m1",providerSwitch:{targetHarness:"pi"}}).catch(e => e);
		});
		expect(failure).toBe(unknown);
		expect(failure).not.toBeInstanceOf(ProviderSwitchFailed);
	});
	it("also identifies explicit daemon 503 refusal when switching with an attachment", async () => {
		post.mockResolvedValue({ data: undefined, error: unavailable, response: new Response(null, {status:503}) });
		const { result } = renderHook(() => useProviderSwitchCommand("ao-1"), {wrapper});
		let failure: unknown;
		await act(async () => {
			failure = await result.current.mutateAsync({targetHarness:"pi"}).catch(e => e);
		});
		expect(failure).toBeInstanceOf(ProviderSwitchFailed);
	});
});
