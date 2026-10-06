import { useCanGoBack, useRouter } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, PanelLeft } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarIsVisible, useUiStore } from "../stores/ui-store";
import { AOMascot } from "./AOMascot";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();
const noDragStyle = isMac
  ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties)
  : undefined;

// Sidebar chrome cluster (sidebar toggle + history arrows). It stays fixed while
// the sidebar expands or collapses. macOS pins it beside the traffic lights;
// Linux has no traffic lights, so it sits at the sidebar's top-left. (Windows
// keeps these controls in its own titlebar.)
// The installed router has no useCanGoForward, and deriving one as
// `__TSR_index < history.length - 1` (the upstream hook's approach) is wrong
// here: window.history.length also counts entries the router never created —
// the WebContents' initial blank entry, pre-router loads — so the tip of the
// stack still reads as "forward available" and the arrow no-ops. Instead,
// track the highest router index reachable on the live stack: a PUSH discards
// the forward entries (the new index is the tip); BACK/FORWARD/GO only move
// within it. After a mid-stack reload the tip resets to the current entry —
// forward greys out rather than dangle on entries we can no longer see.
export function useCanGoForward(): boolean {
  const router = useRouter();
  const [canGoForward, setCanGoForward] = useState(false);
  useEffect(() => {
    let tip = router.history.location.state.__TSR_index;
    return router.history.subscribe(({ location, action }) => {
      const index = location.state.__TSR_index;
      tip = action.type === "PUSH" ? index : Math.max(tip, index);
      setCanGoForward(index < tip);
    });
  }, [router]);
  return canGoForward;
}

// Reveals the history arrows while the pointer is over the sidebar body or the
// button cluster. The rest of the titlebar band stays a window-drag region
// (double-click to maximize), so it cannot report hover. A short leave delay
// absorbs the one-frame gap when the pointer crosses between the two.
const REVEAL_LEAVE_DELAY_MS = 60;

function useSidebarReveal(isSidebarOpen: boolean) {
  const [revealed, setRevealed] = useState(false);
  const inside = useRef({ zone: false, sidebar: false });
  const timer = useRef<number | undefined>(undefined);

  const update = useCallback((source: "zone" | "sidebar", value: boolean) => {
    inside.current[source] = value;
    window.clearTimeout(timer.current);
    if (inside.current.zone || inside.current.sidebar) {
      setRevealed(true);
    } else {
      timer.current = window.setTimeout(() => setRevealed(false), REVEAL_LEAVE_DELAY_MS);
    }
  }, []);

  useEffect(() => {
    if (!isSidebarOpen) {
      inside.current = { zone: false, sidebar: false };
      window.clearTimeout(timer.current);
      setRevealed(false);
      return;
    }
    let el: HTMLElement | null = null;
    const enter = () => update("sidebar", true);
    const leave = () => update("sidebar", false);
    const frame = requestAnimationFrame(() => {
      el = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
      if (!el) return;
      el.addEventListener("pointerenter", enter);
      el.addEventListener("pointerleave", leave);
    });
    return () => {
      cancelAnimationFrame(frame);
      window.clearTimeout(timer.current);
      el?.removeEventListener("pointerenter", enter);
      el?.removeEventListener("pointerleave", leave);
    };
  }, [isSidebarOpen, update]);

  return {
    revealed,
    onZoneEnter: () => update("zone", true),
    onZoneLeave: () => update("zone", false),
  };
}

// The brand replaces the arrows only once the sidebar has fully slid open, and
// the arrows come back the moment it starts closing. The sidebar animates with
// a spring (no transitionend), so watch its container reach x = 0.
const SETTLE_TIMEOUT_MS = 1500;

function useSidebarSettledOpen(isSidebarOpen: boolean): boolean {
  const [settled, setSettled] = useState(false);
  useEffect(() => {
    if (!isSidebarOpen) {
      setSettled(false);
      return;
    }
    const started = performance.now();
    let frame = 0;
    const check = () => {
      const el = document.querySelector<HTMLElement>(
        '[data-slot="sidebar-container"]',
      );
      const open = el ? el.getBoundingClientRect().left >= -0.5 : false;
      if (open || performance.now() - started > SETTLE_TIMEOUT_MS) {
        setSettled(true);
        return;
      }
      frame = requestAnimationFrame(check);
    };
    frame = requestAnimationFrame(check);
    return () => cancelAnimationFrame(frame);
  }, [isSidebarOpen]);
  return isSidebarOpen && settled;
}

export function TitlebarNav({
  historyLocked = false,
  isFullScreen = false,
}: {
  historyLocked?: boolean;
  isFullScreen?: boolean;
}) {
  const { t } = useTranslation();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const isSidebarOpen = useUiStore(sidebarIsVisible);
  const router = useRouter();
  const canGoBack = useCanGoBack();
  const canGoForward = useCanGoForward();
  const { revealed, onZoneEnter, onZoneLeave } =
    useSidebarReveal(isSidebarOpen);
  const showBrand = useSidebarSettledOpen(isSidebarOpen);
  const [keyboardFocus, setKeyboardFocus] = useState(false);
  // The sidebar's minimum width is measured from the brand label, which only
  // exists once this mounts it. Nudge the sidebar's resize re-clamp so a stored
  // or default width narrower than the label grows to fit it.
  useEffect(() => {
    if (showBrand) window.dispatchEvent(new Event("resize"));
  }, [showBrand]);

  if (!isMac && !isLinux) return null;
  // Native fullscreen changes only the horizontal traffic-light reserve.
  // Sidebar and route state must never move the navigation centerline.
  const leftClass = !isMac
    ? isSidebarOpen
      ? "left-titlebar-cluster-left-linux"
      : "left-titlebar-cluster-left-linux-panel"
    : isFullScreen
      ? "left-titlebar-cluster-left-fullscreen"
      : "left-titlebar-cluster-left";
  const topClass = isMac ? "top-px" : "top-0.75";
  const heightClass = "h-traffic-light-clearance";

  // With the sidebar open the brand and the history arrows share one slot: the
  // brand shows at rest, the arrows while the pointer is over the sidebar or the
  // cluster (or on keyboard focus). Collapsed, there is no brand, so the arrows
  // stay put. The arrows are only mounted while visible, so hidden arrows never
  // leave an invisible no-drag hole in the window-drag region.
  const arrowsVisible = !showBrand || revealed || keyboardFocus;

  return (
    <div
      className={`fixed ${topClass} ${leftClass} z-titlebar flex ${heightClass} items-center gap-1`}
      data-slot="titlebar-nav"
      onBlur={() => setKeyboardFocus(false)}
      onFocus={(event) =>
        setKeyboardFocus(event.target.matches(":focus-visible"))
      }
      onPointerEnter={onZoneEnter}
      onPointerLeave={onZoneLeave}
    >
      <TitlebarButton
        label={
          isSidebarOpen ? t("shell.collapseSidebar") : t("shell.expandSidebar")
        }
        onClick={toggleSidebar}
        title={
          isSidebarOpen
            ? t("titlebar.collapseSidebarShortcut")
            : t("titlebar.expandSidebarShortcut")
        }
      >
        <PanelLeft className="size-icon-lg" aria-hidden="true" />
      </TitlebarButton>
      <div className="grid items-center">
        {showBrand ? (
          // Not a button on purpose: it stays part of the window-drag region.
          // `invisible` (not unmounted) keeps its width, which both holds the
          // slot steady and feeds the sidebar's minimum-width measurement.
          <span
            className={`col-start-1 row-start-1 ml-1.5 inline-flex select-none items-center gap-1.5 whitespace-nowrap px-0.5 text-base font-semibold leading-tight tracking-tight-lg text-foreground ${
              arrowsVisible ? "invisible" : ""
            }`}
            data-sidebar-brand=""
          >
            <AOMascot className="h-5.5 w-5.5 shrink-0" />
            Orchestrator.inc
          </span>
        ) : null}
        {arrowsVisible ? (
          <div className="col-start-1 row-start-1 flex items-center gap-1">
            <TitlebarButton
              disabled={historyLocked || !canGoBack}
              label={t("titlebar.goBack")}
              onClick={() => router.history.back()}
              title={t("titlebar.goBack")}
            >
              <ArrowLeft className="size-icon-lg" aria-hidden="true" />
            </TitlebarButton>
            <TitlebarButton
              disabled={historyLocked || !canGoForward}
              label={t("titlebar.goForward")}
              onClick={() => router.history.forward()}
              title={t("titlebar.goForward")}
            >
              <ArrowRight className="size-icon-lg" aria-hidden="true" />
            </TitlebarButton>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function TitlebarButton({
  label,
  title,
  disabled,
  tabIndex,
  onClick,
  children,
}: {
  label: string;
  title: string;
  disabled?: boolean;
  tabIndex?: number;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <button
            aria-label={label}
            aria-disabled={disabled || undefined}
            className="grid size-control-md place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-55 disabled:hover:bg-transparent disabled:hover:text-passive"
            disabled={disabled}
            onClick={onClick}
            style={noDragStyle}
            tabIndex={tabIndex}
            type="button"
          >
            {children}
          </button>
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom">{title}</TooltipContent>
    </Tooltip>
  );
}
