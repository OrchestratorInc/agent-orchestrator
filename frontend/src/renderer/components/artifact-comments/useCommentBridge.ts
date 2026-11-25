import { useCallback, useEffect, useRef } from "react";
import {
	ARTIFACT_COMMENTS_CONNECT,
	ARTIFACT_COMMENTS_PROTOCOL_VERSION,
	parseBridgeToViewerMessage,
	type ArtifactCommentsConnectMessage,
	type BridgePickedAnchor,
	type BridgeSelection,
	type ViewerToBridgeMessage,
} from "../../../shared/artifact-comments";


export type CommentBridgeHandlers = {
	onPick?: (anchor: BridgePickedAnchor) => void;
	onMarker?: (path: string) => void;
	/** Where the selected element is in the frame's viewport; path null when nothing is. */
	onSelection?: (selection: BridgeSelection) => void;
	onEscape?: () => void;
};

/** A CSS colour resolved to rgb(), since the frame is another origin and can't read our variables. */
function resolveColor(value: string): string {
	const probe = document.createElement("span");
	probe.style.color = value;
	probe.style.display = "none";
	document.body.append(probe);
	const color = getComputedStyle(probe).color;
	probe.remove();
	return color || value;
}

/** The accent the bridge draws with (the Orchestrate blue, as on the comment badge) and its ~14% tint. */
export function commentBridgeTheme(): { accent: string; tint: string } {
	return {
		accent: resolveColor("var(--color-brand-logo)"),
		tint: resolveColor("color-mix(in oklch, var(--color-brand-logo) 14%, transparent)"),
	};
}

/**
 * Talks to the comments bridge inside a published artifact frame. On every
 * load of the frame it hands the bridge a private MessagePort, and from then
 * on uses only that port: window messages from the frame (or anything else)
 * are never read. Messages carry positions only, never comment text.
 */
export function useCommentBridge(frame: HTMLIFrameElement | null, handlers: CommentBridgeHandlers) {
	const portRef = useRef<MessagePort | null>(null);
	const readyRef = useRef(false);
	// What the bridge should currently show, replayed after each (re)connect.
	const stateRef = useRef<{ mode?: ViewerToBridgeMessage; anchors?: ViewerToBridgeMessage; select?: ViewerToBridgeMessage }>({});
	const handlersRef = useRef(handlers);
	handlersRef.current = handlers;

	const post = useCallback((message: ViewerToBridgeMessage) => {
		if (readyRef.current) portRef.current?.postMessage(message);
	}, []);

	useEffect(() => {
		if (!frame) return;
		const connect = () => {
			portRef.current?.close();
			readyRef.current = false;
			const target = frame.contentWindow;
			if (!target) return;
			const channel = new MessageChannel();
			portRef.current = channel.port1;
			channel.port1.onmessage = (event: MessageEvent) => {
				const message = parseBridgeToViewerMessage(event.data);
				if (!message) return;
				if (message.type === "ready") {
					if (message.protocol !== ARTIFACT_COMMENTS_PROTOCOL_VERSION) return;
					readyRef.current = true;
					channel.port1.postMessage({ type: "theme", ...commentBridgeTheme() } satisfies ViewerToBridgeMessage);
					const { mode, anchors, select } = stateRef.current;
					for (const replay of [mode, anchors, select]) if (replay) channel.port1.postMessage(replay);
					return;
				}
				if (!readyRef.current) return;
				if (message.type === "pick") handlersRef.current.onPick?.(message.anchor);
				else if (message.type === "marker") handlersRef.current.onMarker?.(message.path);
				else if (message.type === "selection") handlersRef.current.onSelection?.({ path: message.path, top: message.top, bottom: message.bottom });
				else if (message.type === "escape") handlersRef.current.onEscape?.();
			};
			// The sandboxed page has an opaque origin, so "*" is the only target that reaches it.
			target.postMessage({ type: ARTIFACT_COMMENTS_CONNECT, protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION } satisfies ArtifactCommentsConnectMessage, "*", [channel.port2]);
		};
		frame.addEventListener("load", connect);
		return () => {
			frame.removeEventListener("load", connect);
			portRef.current?.close();
			portRef.current = null;
			readyRef.current = false;
		};
	}, [frame]);

	const setMode = useCallback((message: Extract<ViewerToBridgeMessage, { type: "mode" }>) => {
		stateRef.current.mode = message;
		post(message);
	}, [post]);
	const setAnchors = useCallback((anchors: Extract<ViewerToBridgeMessage, { type: "anchors" }>["anchors"]) => {
		const message: ViewerToBridgeMessage = { type: "anchors", anchors };
		stateRef.current.anchors = message;
		post(message);
	}, [post]);
	const select = useCallback((path: string | null) => {
		const message: ViewerToBridgeMessage = path ? { type: "select", path } : { type: "clear" };
		stateRef.current.select = path ? message : undefined;
		post(message);
	}, [post]);

	return { setMode, setAnchors, select };
}
