import { useLayoutEffect, useRef, useState } from "react";
import { workerProjectForEnvironment } from "./worker-controls";

/** Apply selections on tap; only search waits for the native sheet to dismiss. */
export function useWorkerControlsDraft({ open, selectedEnvironmentId, selectedProjectId, projects, onSelectEnvironment, onSelectProject, onDismiss, onSearch, searchDelayMs = 0 }: {
	open: boolean;
	selectedEnvironmentId: string;
	selectedProjectId: string;
	projects: { id: string; name: string; hostId?: string; hostName?: string }[];
	onSelectEnvironment(id: string): void;
	onSelectProject(id: string): void;
	onDismiss(): void;
	onSearch(): void;
	/** Extra wait after onClose before focusing search; see the Android sheet. */
	searchDelayMs?: number;
}) {
	const [environmentId, setEnvironmentId] = useState(selectedEnvironmentId);
	const [projectId, setProjectId] = useState(selectedProjectId);
	const wasOpen = useRef(false);
	const closed = useRef(!open);
	const searchAfterClose = useRef(false);
	useLayoutEffect(() => {
		if (open && !wasOpen.current) {
			setEnvironmentId(selectedEnvironmentId);
			setProjectId(selectedProjectId);
			closed.current = false;
			searchAfterClose.current = false;
		}
		wasOpen.current = open;
	}, [open, selectedEnvironmentId, selectedProjectId]);
	return {
		environmentId,
		projectId,
		selectEnvironment(id: string) {
			const nextProjectId = workerProjectForEnvironment(projects, id, projectId);
			setEnvironmentId(id);
			setProjectId(nextProjectId);
			if (id !== selectedEnvironmentId) onSelectEnvironment(id);
			if (nextProjectId !== selectedProjectId) onSelectProject(nextProjectId);
			onDismiss();
		},
		selectProject(id: string) {
			setProjectId(id);
			if (id !== selectedProjectId) onSelectProject(id);
			onDismiss();
		},
		close: onDismiss,
		search() { searchAfterClose.current = true; onDismiss(); },
		didClose() {
			if (closed.current) return;
			closed.current = true;
			// iOS fires onClose after the SwiftUI dismissal finishes. Android fires it
			// as soon as a programmatic close starts, hence searchDelayMs there.
			onDismiss();
			if (!searchAfterClose.current) return;
			if (searchDelayMs > 0) setTimeout(onSearch, searchDelayMs);
			else onSearch();
		},
	};
}
