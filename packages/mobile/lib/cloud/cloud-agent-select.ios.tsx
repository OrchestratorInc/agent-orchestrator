import { Host } from "@expo/ui";
import { Button, HStack, Image, Menu, Spacer, Text } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, accessibilityLabel, buttonStyle, contentShape, font, foregroundStyle, frame, lineLimit, menuOrder, opacity, shapes, tint, truncationMode } from "@expo/ui/swift-ui/modifiers";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { StyleSheet, View } from "react-native";
import { haptics } from "../haptics";
import { HarnessImage, useHarnessLogoUris } from "../spawn-composer-controls.ios";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, press, space, touchTarget, type } from "../tokens";
import type { CloudAgentSelectProps } from "./cloud-agent-select";

export type { CloudAgentSelectProps } from "./cloud-agent-select";

/** The same branded, native menu as task creation, anchored to the selected agent. */
export function CloudAgentSelect({ label, value, options, onChange, enabled, separator = false }: CloudAgentSelectProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const logoOptions = useMemo(() => options.map((option) => ({ id: option.value, label: option.label })), [options]);
	const logoUris = useHarnessLogoUris(logoOptions);
	const selected = options.find((option) => option.value === value);
	const selectedLabel = selected?.label ?? "Choose agent";
	const canSelect = enabled && options.length > 0;
	const current = useRef({ value, options, onChange, enabled });
	current.current = { value, options, onChange, enabled };
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	const choose = useCallback((next: string) => {
		const latest = current.current;
		// A dismissed native menu can still deliver a queued selection.
		if (!mounted.current || !latest.enabled || next === latest.value || !latest.options.some((option) => option.value === next)) return;
		haptics.select(); latest.onChange(next);
	}, []);
	const selectedContent = <HStack spacing={space.sm} modifiers={[frame({ height: touchTarget }), contentShape(shapes.rectangle())]}>
		{selected ? <HarnessImage uri={logoUris[selected.value]} harness={selected.value} /> : null}
		<Text modifiers={[font({ size: type.callout.fontSize }), lineLimit(1), truncationMode("tail"), foregroundStyle(t.textSecondary)]}>{selectedLabel}</Text>
		{canSelect ? <Image systemName="chevron.up.chevron.down" size={iconSize.xs} /> : null}
	</HStack>;

	return <View style={[styles.row, separator && { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle }]}>
		<Host matchContents={{ vertical: true }} style={styles.host} colorScheme={scheme} seedColor={t.accent}>
			<HStack spacing={space.md} modifiers={[frame({ minHeight: touchTarget }), opacity(canSelect ? 1 : press.opacity)]}>
				<Text modifiers={[font({ size: type.callout.fontSize }), foregroundStyle(t.textPrimary)]}>{label.replace(/ agent$/i, "")}</Text>
				<Spacer />
				{canSelect ? <Menu label={selectedContent} modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityLabel(`${label}: ${selectedLabel}`), accessibilityIdentifier(label)]}>
					{options.map((option) => <Button key={option.value} onPress={() => choose(option.value)}>
						<HStack spacing={space.sm}>
							<HarnessImage uri={logoUris[option.value]} harness={option.value} />
							<Text>{option.label}</Text>
							<Spacer />
							{option.value === selected?.value ? <Image systemName="checkmark" size={iconSize.xs} /> : null}
						</HStack>
					</Button>)}
				</Menu> : selectedContent}
			</HStack>
		</Host>
	</View>;
}

const styles = StyleSheet.create({
	row: { width: "100%", paddingHorizontal: space.lg, paddingVertical: space.sm },
	host: { width: "100%" },
});
