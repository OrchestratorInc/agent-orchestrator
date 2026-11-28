import { QueryClientContext } from "@tanstack/react-query";
import { useCallback, useContext, useRef, useState } from "react";
import { commitSessionRename } from "../lib/commit-session-rename";
import type { WorkspaceSession } from "../types/workspace";

export const MAX_SESSION_DISPLAY_NAME_LEN = 100;

type RenameableSession = Pick<WorkspaceSession, "id" | "title" | "hostId" | "cloud">;

export function useSessionRename(session?: RenameableSession, onRenamed?: () => void | Promise<void>) {
  // The cache carries the new title to every list; a tab outside a query
  // provider still renames, showing it once the next read lands.
  const queryClient = useContext(QueryClientContext) ?? null;
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
    // Starts the rename before any await, so the editor closes onto the new title.
    await commitSessionRename(queryClient, session, draft, onRenamed ? { refresh: onRenamed } : undefined);
  }, [draft, onRenamed, queryClient, session]);

  return { begin, cancel, commit, draft, isEditing, setDraft };
}
