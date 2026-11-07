import { create } from "zustand";
import type { OperatorVerbRequest } from "../../main/operator-ipc";

/** 只保留工作台位置；业务状态始终由 daemon 快照提供。 */
export const useWorkflowUi = create<{
	open: boolean;
	workflowId: string;
	taskId: string;
	drafts: Record<string, string>;
	requests: Record<string, { payload: string; request: OperatorVerbRequest }>;
	setDraft: (key: string, value: string) => void;
	rememberRequest: (key: string, payload: string, request: OperatorVerbRequest) => void;
	forgetRequest: (key: string) => void;
	setOpen: (open: boolean) => void;
	selectWorkflow: (workflowId: string) => void;
	selectTask: (taskId: string) => void;
}>((set) => ({
	open: false, workflowId: "", taskId: "",
	drafts: {}, requests: {},
	setDraft: (key, value) => set((state) => ({ drafts: { ...state.drafts, [key]: value } })),
	rememberRequest: (key, payload, request) => set((state) => ({ requests: { ...state.requests, [key]: { payload, request } } })),
	forgetRequest: (key) => set((state) => {
		const requests = { ...state.requests };
		delete requests[key];
		return { requests };
	}),
	setOpen: (open) => set({ open }),
	selectWorkflow: (workflowId) => set({ workflowId, taskId: "" }),
	selectTask: (taskId) => set({ taskId }),
}));

/** 草稿按 daemon 和资源隔离；切换模式与页签不会丢失尚未提交的输入。 */
export function useWorkflowDraft<T>(key: string, initial: T): [T, (value: T) => void] {
	const encoded = useWorkflowUi(state => state.drafts[key]);
	return [encoded === undefined ? initial : JSON.parse(encoded) as T, value => useWorkflowUi.getState().setDraft(key, JSON.stringify(value))];
}
