import * as Clipboard from "expo-clipboard";
import { useRouter } from "expo-router";
import { useCallback } from "react";
import { Share } from "react-native";
import { haptics } from "../haptics";
import type { ToastController } from "../Toast.types";
import type { FileRowAction } from "./FileRowMenu.types";

/**
 * How the Files screens open what a row points at, and what its long-press
 * menu does. A folder is a page of its own, so the system back (the edge
 * swipe, Android's back, the back button's history) climbs one level at a
 * time instead of leaving Files.
 */
export function useFileActions({ sessionId, hostId, toast }: { sessionId: string; hostId?: string; toast: Pick<ToastController, "show"> }) {
	// `show` is stable where the controller is not, so the rows keep their memo.
	const showToast = toast.show;
	const router = useRouter();
	const openFile = useCallback((path: string, mode: "diff" | "file", previousPath?: string) => {
		router.push({ pathname: "/files/[sessionId]/file", params: { sessionId, path, mode, previousPath, hostId } });
	}, [hostId, router, sessionId]);
	const openFolder = useCallback((dir: string) => {
		router.push({ pathname: "/files/[sessionId]/folder", params: { sessionId, dir, hostId } });
	}, [hostId, router, sessionId]);
	/** The queued review, in a sheet over the page. */
	const openReview = useCallback(() => {
		router.push({ pathname: "/files/[sessionId]/review", params: { sessionId, hostId } });
	}, [hostId, router, sessionId]);
	const act = useCallback((action: FileRowAction, file: { path: string; previousPath?: string }) => {
		switch (action) {
			case "openDiff":
				openFile(file.path, "diff", file.previousPath);
				return;
			case "openFile":
				openFile(file.path, "file");
				return;
			case "copyPath":
				void Clipboard.setStringAsync(file.path);
				haptics.success();
				showToast("Path copied.");
				return;
			case "share":
				// Dismissing the share sheet resolves; only a sheet that cannot open rejects.
				void Share.share({ message: file.path }).catch(() => showToast("Couldn't open the share sheet."));
				return;
		}
	}, [openFile, showToast]);
	return { openFile, openFolder, openReview, act };
}
