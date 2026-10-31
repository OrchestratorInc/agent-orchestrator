import { useCallback, useRef, useState } from "react";
import { renameSession } from "../lib/rename-session";
import { canRenameSession, type WorkspaceSession } from "../types/workspace";

export const MAX_SESSION_DISPLAY_NAME_LEN = 100;

type RenameableSession = Pick<WorkspaceSession, "id" | "title" | "hostId" | "kind" | "cloud">;

// A Cloud session or the project orchestrator never enters rename, whichever
// surface asks; `canRename` tells the caller whether to offer it at all.
export function useSessionRename(candidate?: RenameableSession, onRenamed?: () => void | Promise<void>) {
  const session = candidate && canRenameSession(candidate) ? candidate : undefined;
  const [isEditing, setIsEditing] = useState(false);
  const [draft, setDraft] = useState(session?.title ?? "");
  const cancelledRef = useRef(false);

  const begin = useCallback(() => {
    if (!session) return;
    cancelledRef.current = false;
    setDraft(session.title);
    setIsEditing(true);
  }, [session]);

  const cancel = useCallback(() => {
    cancelledRef.current = true;
    setDraft(session?.title ?? "");
    setIsEditing(false);
  }, [session?.title]);

  const commit = useCallback(async () => {
    if (cancelledRef.current) {
      cancelledRef.current = false;
      setIsEditing(false);
      return;
    }
    setIsEditing(false);
    if (!session) return;
    const name = draft.trim();
    if (!name || name === session.title) return;
		try {
			if (session.hostId) await renameSession(session.id, name, session.hostId);
			else await renameSession(session.id, name);
			await onRenamed?.();
		} catch (error) {
      console.error("Failed to rename session:", error);
    }
	}, [draft, onRenamed, session]);

  return { begin, canRename: Boolean(session), cancel, commit, draft, isEditing, setDraft };
}
