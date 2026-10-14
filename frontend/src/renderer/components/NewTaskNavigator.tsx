import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";

// Every New Task entry point (⌘N / Ctrl+Shift+N, sidebar, topbar, board, home,
// command palette) raises the ui-store `newTaskRequest` signal. The shell is
// mounted on every route, so this opens the new chat page from anywhere; the
// nonce lets a repeat request for the same project fire again.
export function NewTaskNavigator() {
	const navigate = useNavigate();
	const newTaskRequest = useUiStore((state) => state.newTaskRequest);
	const lastNonce = useRef(newTaskRequest?.nonce ?? 0);

	useEffect(() => {
		if (!newTaskRequest || newTaskRequest.nonce === lastNonce.current) return;
		lastNonce.current = newTaskRequest.nonce;
		const { projectId, hostId } = newTaskRequest;
		void navigate({
			to: "/new",
			search: {
				project: projectId === STANDALONE_WORKSPACE_ID ? undefined : projectId,
				host: hostId,
			},
		});
	}, [navigate, newTaskRequest]);

	return null;
}
