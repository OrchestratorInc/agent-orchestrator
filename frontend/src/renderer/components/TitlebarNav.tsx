import { motion, useReducedMotion } from "motion/react";
import { useCanGoBack, useRouter } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, PanelLeft } from "@/components/app-icons";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SHELL_PANEL_SPRING } from "@/lib/motion-spring";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { sidebarIsVisible, useUiStore } from "../stores/ui-store";
import { AOMascot } from "./AOMascot";
import type { AppShortcutId } from "../../shared/shortcuts";
import { BuildInfoTooltip } from "./BuildInfoTooltip";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();
const noDragStyle = isMac
  ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties)
  : undefined;

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

export function TitlebarNav({
  onBack,
  historyLocked = false,
  isFullScreen = false,
  zoomFactor = 1,
}: {
  onBack?: () => void;
  historyLocked?: boolean;
  isFullScreen?: boolean;
  zoomFactor?: number;
}) {
  const { t } = useTranslation();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const isSidebarOpen = useUiStore(sidebarIsVisible);
  const router = useRouter();
  const canGoBack = useCanGoBack();
  const canGoForward = useCanGoForward();
  const showBrand = isSidebarOpen;
  useEffect(() => {
    if (showBrand) window.dispatchEvent(new Event("resize"));
  }, [showBrand, isFullScreen, zoomFactor]);

  if (!isMac && !isLinux) return null;
  const leftClass = !isMac
    ? isSidebarOpen
      ? "left-titlebar-cluster-left-linux"
      : "left-titlebar-cluster-left-linux-panel"
    : isFullScreen
      ? "left-titlebar-cluster-left-fullscreen"
      : "left-titlebar-cluster-left";
  const topClass = isMac ? "top-0" : "top-0.75";
  const heightClass = isMac && !isFullScreen ? "h-traffic-light-clearance" : "h-toolbar";

  const arrowsVisible = !showBrand;
  const prefersReducedMotion = useReducedMotion();
  const transition = prefersReducedMotion ? { duration: 0 } : SHELL_PANEL_SPRING;

  const brandRef = useRef<HTMLSpanElement>(null);
  const [brandMaxWidth, setBrandMaxWidth] = useState<number | undefined>(undefined);
  useEffect(() => {
    if (!showBrand) return;
    const sidebar = document.querySelector<HTMLElement>('[data-slot="sidebar-container"]');
    if (!sidebar) return;
    const fit = () => {
      const brand = brandRef.current;
      if (!brand) return;
      const available = sidebar.offsetWidth - brand.getBoundingClientRect().left - 12;
      setBrandMaxWidth(Math.max(24, Math.floor(available)));
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(sidebar);
    window.addEventListener("resize", fit);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", fit);
    };
  }, [showBrand]);

  return (
    <div
      className={`fixed ${topClass} ${leftClass} z-titlebar flex ${heightClass} items-center gap-1`}
      data-slot="titlebar-nav"
    >
      <TitlebarButton
        label={
          isSidebarOpen ? t("shell.collapseSidebar") : t("shell.expandSidebar")
        }
        onClick={toggleSidebar}
        shortcut="toggle-sidebar"
      >
        <PanelLeft className="size-icon-lg" aria-hidden="true" />
      </TitlebarButton>
      <div className="flex items-center">
        <motion.div
          animate={{ width: showBrand ? "auto" : 0, opacity: showBrand ? 1 : 0 }}
          aria-hidden={showBrand ? undefined : true}
          className="overflow-hidden"
          data-slot="titlebar-brand"
          initial={false}
          inert={!showBrand}
          transition={transition}
        >
          <span
            className="flex w-max select-none items-center gap-1.5 whitespace-nowrap text-base font-semibold leading-tight tracking-tight-lg text-foreground"
            data-sidebar-brand=""
            ref={brandRef}
            style={{ maxWidth: brandMaxWidth }}
          >
            <BuildInfoTooltip>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <AOMascot className="h-5.5 w-5.5 shrink-0" />
                <span className="min-w-0 truncate" data-brand-label="">Orchestrator</span>
              </span>
            </BuildInfoTooltip>
          </span>
        </motion.div>
        <motion.div
          animate={{ width: arrowsVisible ? "auto" : 0, opacity: arrowsVisible ? 1 : 0 }}
          aria-hidden={arrowsVisible ? undefined : true}
          className="overflow-hidden"
          data-slot="titlebar-history"
          initial={false}
          inert={!arrowsVisible}
          transition={transition}
        >
          <div className="flex items-center gap-1">
            <TitlebarButton
              disabled={!onBack && (historyLocked || !canGoBack)}
              label={t("titlebar.goBack")}
              onClick={onBack ?? (() => router.history.back())}
              shortcut="history-back"
            >
              <ArrowLeft className="size-icon-lg" aria-hidden="true" />
            </TitlebarButton>
            <TitlebarButton
              disabled={historyLocked || !canGoForward}
              label={t("titlebar.goForward")}
              onClick={() => router.history.forward()}
              shortcut="history-forward"
            >
              <ArrowRight className="size-icon-lg" aria-hidden="true" />
            </TitlebarButton>
          </div>
        </motion.div>
      </div>
    </div>
  );
}

function TitlebarButton({
  label,
  shortcut,
  disabled,
  tabIndex,
  onClick,
  children,
}: {
  label: string;
  shortcut: AppShortcutId;
  disabled?: boolean;
  tabIndex?: number;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <Button
            aria-label={label}
            aria-disabled={disabled || undefined}
            className="disabled:opacity-55"
            disabled={disabled}
            onClick={onClick}
            size="icon-sm"
            style={noDragStyle}
            tabIndex={tabIndex}
            type="button"
            variant="quiet"
          >
            {children}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent shortcut={shortcut} side="bottom">{label}</TooltipContent>
    </Tooltip>
  );
}
