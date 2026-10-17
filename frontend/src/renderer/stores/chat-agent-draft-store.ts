import { create } from "zustand";

// The agent a chat will continue with, picked from the model menu but not sent
// yet. Picking only changes this, so it is instant and free to change again; the
// next send carries it and switches the session (useSwitchAgent).
//
// Keyed by sessionUiKey. Not persisted: a pick nobody sent is not worth keeping
// across a reload.
export type ChatAgentDraft = {
	harness: string;
	/** Empty: the agent's default model. */
	model: string;
	/** Empty: the model's default level. */
	effort: string;
};

type ChatAgentDraftState = {
	drafts: Record<string, ChatAgentDraft>;
	setDraft: (sessionKey: string, draft: ChatAgentDraft | undefined) => void;
};

export const useChatAgentDraftStore = create<ChatAgentDraftState>((set) => ({
	drafts: {},
	setDraft: (sessionKey, draft) =>
		set((state) => {
			if (!draft) {
				if (!(sessionKey in state.drafts)) return state;
				const { [sessionKey]: _dropped, ...rest } = state.drafts;
				return { drafts: rest };
			}
			return { drafts: { ...state.drafts, [sessionKey]: draft } };
		}),
}));
