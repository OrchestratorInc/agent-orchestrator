"use client";

import {
  COMPANY,
  DOWNLOAD_URL_LINUX,
  DOWNLOAD_URL_MAC_ARM64,
  DOWNLOAD_URL_MAC_X64,
  DOWNLOAD_URL_WINDOWS,
} from "@ao/shared/constants";
import { FaApple, FaLinux, FaWindows } from "react-icons/fa";
import { isMacPlatform, Platform, usePlatform } from "../hooks/useOS";

export type DownloadTarget = {
  href: string;
  label: string;
  icon: "apple" | "windows" | "linux";
};

export function getDownloadTarget(platform: Platform): DownloadTarget {
  if (platform === Platform.Windows) {
    return {
      href: DOWNLOAD_URL_WINDOWS,
      label: "Download for Windows",
      icon: "windows",
    };
  }

  if (platform === Platform.Linux) {
    return {
      href: DOWNLOAD_URL_LINUX,
      label: "Download for Linux",
      icon: "linux",
    };
  }

  if (platform === Platform.MacIntel) {
    return {
      href: DOWNLOAD_URL_MAC_X64,
      label: "Download for macOS (Intel)",
      icon: "apple",
    };
  }

  if (platform === Platform.Mobile) {
    return {
      href: `${COMPANY.DOCS_URL}/installation/`,
      label: "Open install guide",
      icon: "apple",
    };
  }

  if (isMacPlatform(platform)) {
    return {
      href: DOWNLOAD_URL_MAC_ARM64,
      label: "Download for macOS",
      icon: "apple",
    };
  }

  return {
    href: DOWNLOAD_URL_MAC_ARM64,
    label: "Download for macOS",
    icon: "apple",
  };
}

export function PlatformDownloadButton() {
  const { platform } = usePlatform();
  const target = getDownloadTarget(platform);
  const Icon =
    target.icon === "windows"
      ? FaWindows
      : target.icon === "linux"
        ? FaLinux
        : FaApple;

  return (
    <a
      href={target.href}
      className="inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded-3xl bg-foreground px-3 py-2 text-sm font-semibold tracking-[-0.5px] text-background transition-opacity hover:opacity-90 sm:px-6 sm:py-3 sm:text-base"
    >
      <Icon className="size-4 shrink-0" aria-hidden="true" />
      {target.label}
    </a>
  );
}
