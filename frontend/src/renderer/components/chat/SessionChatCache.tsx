import {
	createContext,
	useCallback,
	useContext,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ComponentProps,
	type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { useWorkspaceQuery, useRemoteWorkspaceSnapshots } from "../../hooks/useWorkspaceQuery";
import { refKey } from "../../lib/hosts";
import { SessionViewportContext, sessionViewportState, useSessionViewport } from "../../lib/session-viewport";
import { SessionChatSurface } from "./SessionChatSurface";

type Props = ComponentProps<typeof SessionChatSurface>;
type Entry = { container: HTMLDivElement; props: Props; slot?: HTMLDivElement; embedded: boolean };
type Cache = {
	show: (key: string, slot: HTMLDivElement, props: Props, embedded: boolean) => void;
	park: (key: string, slot: HTMLDivElement) => void;
};
const Context = createContext<Cache | null>(null);

/** Stable portals preserve the composer, transcript viewport and query observers. */
export function SessionChatCacheProvider({ children }: { children: ReactNode }) {
	const entries = useRef(new Map<string, Entry>());
	const parking = useRef<HTMLDivElement>(null);
	const [, revise] = useState(0);
	const workspaces = useWorkspaceQuery();
	const remoteWorkspaces = useRemoteWorkspaceSnapshots();
	const show = useCallback<Cache["show"]>((key, slot, props, embedded) => {
		let entry = entries.current.get(key);
		if (!entry) {
			entry = { container: document.createElement("div"), props, embedded };
			entry.container.className = "h-full min-h-0 w-full overflow-hidden";
			entry.container.dataset.chatCacheKey = key;
			entries.current.set(key, entry);
		}
		if (
			entry.slot === slot &&
			entry.embedded === embedded &&
			Object.keys(props).every((key) => props[key as keyof Props] === entry!.props[key as keyof Props]) &&
			Object.keys(props).length === Object.keys(entry.props).length
		)
			return;
		entry.props = props;
		entry.embedded = embedded;
		entry.slot = slot;
		entry.container.inert = false;
		entry.container.removeAttribute("aria-hidden");
		entry.container.style.contentVisibility = "";
		// Re-appending an already visible node detaches it momentarily in
		// Chromium and collapses the composer's selection during live updates.
		if (entry.container.parentElement !== slot) slot.appendChild(entry.container);
		revise((value) => value + 1);
	}, []);
	const park = useCallback<Cache["park"]>((key, slot) => {
		const entry = entries.current.get(key);
		if (!entry || entry.slot !== slot) return;
		if (document.activeElement instanceof HTMLElement && entry.container.contains(document.activeElement))
			document.activeElement.blur();
		entry.slot = undefined;
		entry.container.inert = true;
		entry.container.setAttribute("aria-hidden", "true");
		entry.container.style.contentVisibility = "hidden";
		parking.current?.appendChild(entry.container);
		revise((value) => value + 1);
	}, []);
	useEffect(() => {
		const sessions = new Map(
			(workspaces.data ?? []).flatMap((workspace) =>
				workspace.sessions.map((session) => [session.id, session] as const),
			),
		);
		let changed = false;
		for (const [key, entry] of entries.current) {
			const remote = entry.props.hostId ? remoteWorkspaces.find((query) => query.hostId === entry.props.hostId) : undefined;
			if (entry.props.hostId ? !remote?.isSuccess : !workspaces.isSuccess) continue;
			const session = entry.props.hostId
				? remote?.data?.flatMap((workspace) => workspace.sessions).find((session) => session.id === entry.props.session.id)
				: sessions.get(entry.props.session.id);
			if (!session || session.mode === "tui") {
				entry.container.remove();
				entries.current.delete(key);
				changed = true;
			} else if (entry.props.session !== session) {
				entry.props = { ...entry.props, session };
				changed = true;
			}
		}
		if (changed) revise((value) => value + 1);
	}, [workspaces.data, workspaces.isSuccess, remoteWorkspaces]);
	useEffect(
		() => () => {
			for (const entry of entries.current.values()) entry.container.remove();
			entries.current.clear();
		},
		[],
	);
	const cache = useMemo(() => ({ show, park }), [show, park]);
	return (
		<Context.Provider value={cache}>
			<div ref={parking} aria-hidden="true" className="pointer-events-none invisible fixed -left-[100000px] top-0" />
			{children}
			{[...entries.current].map(([key, entry]) =>
				createPortal(
					<SessionViewportContext.Provider value={sessionViewportState(entry.embedded, Boolean(entry.slot))}>
						<SessionChatSurface {...entry.props} />
					</SessionViewportContext.Provider>,
					entry.container,
					key,
				),
			)}
		</Context.Provider>
	);
}

export function RetainedSessionChatSurface(props: Props) {
	const cache = useContext(Context);
	const { embedded, visible } = useSessionViewport();
	const slot = useRef<HTMLDivElement>(null);
	const key = refKey({ host: props.hostId ?? "local", id: props.session.id });
	useLayoutEffect(() => {
		if (!cache || !slot.current || !visible) return;
		const element = slot.current;
		return () => cache.park(key, element);
	}, [cache, key, visible]);
	useLayoutEffect(() => {
		if (visible && slot.current) cache?.show(key, slot.current, props, embedded);
	});
	return cache ? <div ref={slot} className="h-full min-h-0 w-full" /> : <SessionChatSurface {...props} />;
}
