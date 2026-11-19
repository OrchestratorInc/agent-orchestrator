import { foregroundStyle } from "@expo/ui/swift-ui/modifiers";

/**
 * SwiftUI's secondary text level, the one a form section footer draws in by
 * default. Built once and shared rather than recreated on every render.
 */
export const secondaryForeground = foregroundStyle({ type: "hierarchical", style: "secondary" });
