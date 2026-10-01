import { memo, useCallback } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
import { ChatComposer } from "./chat/ChatComposer";

// Message box docked under a cloud session's agent terminal. It is the Chat
// composer itself, not a lookalike. A message goes to the control plane whole:
// an idle agent gets it typed into its terminal as one unit (text, then Enter),
// and a busy agent gets it once the running turn ends. In a shared session this
// is how the collaborator works, so two people never interleave keystrokes on
// the same prompt line.
export const CloudSessionComposer = memo(function CloudSessionComposer({
	orgId,
	sessionId,
	agentWorking,
	disabled,
	autoFocus,
}: {
	orgId: string;
	sessionId: string;
	/** The agent is mid-turn, so a message waits until the turn ends. */
	agentWorking: boolean;
	disabled: boolean;
	autoFocus: boolean;
}) {
	const { client } = useCloudCp();
	const send = useCallback(
		async (text: string) => {
			await client.sendSessionMessage(orgId, sessionId, { text });
		},
		[client, orgId, sessionId],
	);
	return (
		<div className="cursor-chat-composer-dock shrink-0 px-4 pt-2 pb-3" data-testid="cloud-session-composer">
			<div className="mx-auto flex w-full max-w-3xl flex-col gap-2">
				<ChatComposer
					onSend={send}
					willQueue={agentWorking}
					disabled={disabled}
					autoFocus={autoFocus}
					autoFocusKey={sessionId}
				/>
			</div>
		</div>
	);
});
