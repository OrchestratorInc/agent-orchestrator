import { useEffect, useReducer, useRef, type ReactNode, type RefObject } from "react";
import { codeSelectionRange } from "./useCodeSelection";

export function hasCodeSelection(container: HTMLElement | null): boolean {
 return Boolean(container && [...container.querySelectorAll<HTMLElement>("diffs-container")].some(host => codeSelectionRange(host)));
}

/** Retain the displayed revision until native selection ends, even for an in-flight refresh. */
export function useSelectionSnapshot<T>(container: RefObject<HTMLDivElement | null>, value: T, identity: string): T {
 const snapshot = useRef({ value, identity });
 const [, refresh] = useReducer(n => n + 1, 0);
 useEffect(() => {
  let selected = hasCodeSelection(container.current);
  const update = () => {
   const next = hasCodeSelection(container.current);
   if (next !== selected) { selected = next; refresh(); }
  };
  document.addEventListener("selectionchange", update);
  document.addEventListener("pointerup", update);
  return () => { document.removeEventListener("selectionchange", update); document.removeEventListener("pointerup", update); };
 }, [container]);
 if (snapshot.current.identity !== identity || !hasCodeSelection(container.current)) snapshot.current = { value, identity };
 return snapshot.current.value;
}

/** Keep Pierre's props stable while its native selection is active. */
export function SelectionStableContent({ container, identity, children }: {
 container: RefObject<HTMLDivElement | null>; identity: string; children: ReactNode;
}) {
 return useSelectionSnapshot(container, children, identity);
}
