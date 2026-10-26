import { Host, RNHostView } from "@expo/ui";
import { Asset } from "expo-asset";
import { Button, Divider, Group, HStack, Image, Label, Menu, ProgressView, Spacer, Text, Toggle, VStack } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	aspectRatio,
	backgroundOverlay,
	buttonStyle,
	clipShape,
	controlSize,
	disabled as disabledModifier,
	font,
	foregroundStyle,
	frame,
	labelStyle,
	layoutPriority,
	lineLimit,
	menuOrder,
	opacity,
	padding,
	resizable,
	tint,
	truncationMode,
} from "@expo/ui/swift-ui/modifiers";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { chipColorFor } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { glassPanel } from "./glass";
import { haptics } from "./haptics";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import type { IOSSymbol } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { iconSize, space, stateOpacity, touchTarget } from "./tokens";
import { MicKey } from "./voice/MicKey";

// SwiftUI's `.infinity` can't cross the bridge, and any width past the sheet's
// does the same job: take all the room the row offers.
const FILL_WIDTH = 10_000;
// Liquid Glass's prominent style only exists from iOS 26; before it SwiftUI
// would fall back to a plain text button, so Start keeps the filled style there.
const startStyle = parseInt(String(Platform.Version), 10) >= 26 ? "glassProminent" : "borderedProminent";

/**
 * Spawn's selectors as SwiftUI menus whose options are the system's checkable
 * items, so the current choice carries a real checkmark and selected state
 * rather than a drawn one. The triggers keep their icon and name, size to their
 * content and scale with Dynamic Type.
 */
export function SpawnComposerControls({
	hosts,
	hostId,
	onSelectHost,
	projects,
	projectId,
	onSelectProject,
	agents,
	harness,
	onSelectHarness,
	models,
	modelSelection,
	modelLabel,
	onSelectModel,
	onChoosePhoto,
	onChooseFile,
	voice,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const logoUris = useHarnessLogoUris(agents);
	const selectedProject = projects.find((project) => project.id === projectId);
	const hostLabel = hosts.find((host) => host.id === hostId)?.label ?? "Choose machine";
	const projectLabel = selectedProject?.label ?? "Choose project";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
	// A choice only fires when it changes, so each one is worth the tick.
	const choose = (apply: (id: string) => void, id: string) => {
		haptics.select();
		apply(id);
	};

	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.host}>
			<VStack alignment="leading" spacing={space.sm}>
				{/* Compact and left-anchored: a max-width frame would make a Menu
				    fill the row, which centers its popup over the whole sheet. */}
				<HStack spacing={space.sm}>
					<Menu
						label={<SelectorLabel icon={<SelectorGlyph name="desktopcomputer" />} title={`Run on ${hostLabel}`} indicator="chevron.up.chevron.down" />}
						modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityIdentifier("spawn-host")]}
					>
						{hosts.map((host) => <Choice key={host.id} label={host.label} selected={host.id === hostId} onChoose={() => choose(onSelectHost, host.id)} />)}
					</Menu>
					<Menu
						label={<SelectorLabel icon={<SelectorGlyph name={projectSystemImage(selectedProject)} />} title={projectLabel} indicator="chevron.up.chevron.down" />}
						modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityIdentifier("spawn-project")]}
					>
						{projects.flatMap((project) => [
							project.sectionBreakBefore ? <Divider key={`${project.id}-break`} /> : null,
							<Choice key={project.id} label={project.label} systemImage={projectSystemImage(project)} selected={project.id === projectId} onChoose={() => choose(onSelectProject, project.id)} />,
						])}
					</Menu>
					<Spacer minLength={0} />
				</HStack>

				<HStack spacing={space.xs} modifiers={[padding({ horizontal: space.sm, vertical: space.xxs }), glassPanel()]}>
					<Menu
						label="Attach"
						systemImage="paperclip"
						modifiers={[
							buttonStyle("plain"),
							labelStyle("iconOnly"),
							menuOrder("fixed"),
							frame({ minWidth: touchTarget, minHeight: touchTarget }),
							font({ textStyle: "body" }),
							tint(t.textPrimary),
							accessibilityIdentifier("spawn-attachment"),
						]}
					>
						<Button label="Choose Photo" systemImage="photo" onPress={onChoosePhoto} />
						<Button label="Choose File" systemImage="doc" onPress={onChooseFile} />
					</Menu>

					<Menu
						label={<SelectorLabel icon={<HarnessImage uri={logoUris[harness]} harness={harness} />} title={harnessLabel} indicator="chevron.down" />}
						modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textPrimary), accessibilityIdentifier("spawn-harness")]}
					>
						{agents.map((agent) => (
							<Choice key={agent.id} label={agent.label} icon={<HarnessImage uri={logoUris[agent.id]} harness={agent.id} />} selected={agent.id === harness} onChoose={() => choose(onSelectHarness, agent.id)} />
						))}
					</Menu>

					<Menu
						label={
							<HStack spacing={space.xxs} modifiers={[frame({ maxWidth: FILL_WIDTH, alignment: "leading" })]}>
								<SelectorText>{modelLabel}</SelectorText>
								<Spacer />
								<SelectorGlyph name="chevron.down" indicator />
							</HStack>
						}
						modifiers={[
							buttonStyle("plain"),
							menuOrder("fixed"),
							frame({ maxWidth: FILL_WIDTH, alignment: "leading" }),
							layoutPriority(1),
							tint(t.textSecondary),
							opacity(models.length || modelSelection === "__auto__" ? 1 : stateOpacity.disabled),
							accessibilityIdentifier("spawn-model"),
						]}
					>
						{[{ id: "__auto__", label: "Automatic" }, ...models].map((model) => (
							<Choice key={model.id} label={model.label} selected={model.id === modelSelection} onChoose={() => choose(onSelectModel, model.id)} />
						))}
					</Menu>

					{/* Hold-to-talk needs press-in and press-out, which a SwiftUI
					    Button doesn't expose, so the mic is the React Native key
					    hosted inside the rail. Plain, like the paperclip: the rail's
					    glass is the material, and a second disc would compete with it. */}
					<RNHostView matchContents>
						<View style={styles.micSlot}>
							<MicKey
								variant="plain"
								size={touchTarget}
								glyphSize={iconSize.md}
								state={voice.state}
								mode={voice.mode}
								onPressIn={voice.onPressIn}
								onPressOut={voice.onPressOut}
							/>
						</View>
					</RNHostView>
				</HStack>

				{/* The success and failure haptics come from the request itself, so
				    the press adds none of its own. */}
				<Button
					onPress={onSpawn}
					testID="spawn-submit"
					modifiers={[
						buttonStyle(startStyle),
						controlSize("large"),
						tint(t.accent),
						disabledModifier(disabled),
						accessibilityLabel(busy ? "Starting task" : "Start task"),
						accessibilityIdentifier("spawn-submit"),
					]}
				>
					<HStack spacing={space.sm} modifiers={[frame({ maxWidth: FILL_WIDTH })]}>
						{busy ? <ProgressView /> : null}
						{/* The accent's own ink while it can be pressed; disabled, the
						    system's dimmed label reads on its grey fill. */}
						<Text modifiers={[font({ textStyle: "headline" }), ...(disabled ? [] : [foregroundStyle(t.onAccent)])]}>
							{busy ? "Starting…" : "Start task"}
						</Text>
					</HStack>
				</Button>
			</VStack>
		</Host>
	);
}

/**
 * One option in a selector menu. A Toggle is the system's checkable menu item:
 * it draws the checkmark and VoiceOver's selected state from `selected` alone,
 * and turning the current choice off again changes nothing.
 */
function Choice({ label, systemImage, icon, selected, onChoose }: {
	label: string;
	systemImage?: IOSSymbol;
	icon?: ReactNode;
	selected: boolean;
	onChoose: () => void;
}) {
	const onIsOnChange = (on: boolean) => { if (on) onChoose(); };
	return icon
		? <Toggle isOn={selected} onIsOnChange={onIsOnChange}><Label title={label} icon={icon} /></Toggle>
		: <Toggle isOn={selected} onIsOnChange={onIsOnChange} label={label} systemImage={systemImage} />;
}

function SelectorLabel({ icon, title, indicator }: { icon: ReactNode; title: string; indicator: IOSSymbol }) {
	return (
		<HStack spacing={space.xs}>
			{icon}
			<SelectorText>{title}</SelectorText>
			<SelectorGlyph name={indicator} indicator />
		</HStack>
	);
}

function SelectorText({ children }: { children: string }) {
	return <Text modifiers={[font({ textStyle: "subheadline", weight: "medium" }), lineLimit(1), truncationMode("tail")]}>{children}</Text>;
}

/** A symbol that scales with the label beside it; the disclosure arrow sits a step smaller. */
function SelectorGlyph({ name, indicator = false }: { name: IOSSymbol; indicator?: boolean }) {
	return <Image systemName={name} modifiers={[font(indicator ? { textStyle: "caption2", weight: "semibold" } : { textStyle: "subheadline" })]} />;
}

function projectSystemImage(project?: SpawnComposerOption): "plus.bubble" | "folder" {
	return project?.icon === "message-square-plus" ? "plus.bubble" : "folder";
}

const styles = StyleSheet.create({
	host: { width: "100%" },
	micSlot: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});

// The mark's box, and the chip it sits on when it needs one to stay visible.
// Same arithmetic as `AgentLogo`: a 16% inset and a 28% corner radius, so a
// chipped mark here and a chipped mark in a row are the same size and shape.
const MARK_SIZE = 20;
const CHIP_INSET = Math.round(MARK_SIZE * 0.16);
const CHIP_RADIUS = Math.round(MARK_SIZE * 0.28);

export function HarnessImage({ uri, harness }: { uri?: string; harness: string }) {
	if (!uri) return <Image systemName="terminal" modifiers={[font({ textStyle: "subheadline" })]} />;
	const mark = [resizable(), aspectRatio({ contentMode: "fit" }), frame({ width: MARK_SIZE - CHIP_INSET * 2, height: MARK_SIZE - CHIP_INSET * 2 })];
	const chip = chipColorFor(harness);
	// opencode's mark is pure white and cursor's likewise, so on the light theme
	// they disappeared entirely — this menu drew the raw asset, where the rest of
	// the app asks `chipColorFor` first. Most marks are colourful and render bare.
	return chip
		? (
			<Group modifiers={[frame({ width: MARK_SIZE, height: MARK_SIZE }), backgroundOverlay({ color: chip }), clipShape("roundedRectangle", CHIP_RADIUS)]}>
				<Image uiImage={uri} modifiers={mark} />
			</Group>
		)
		: <Image uiImage={uri} modifiers={[resizable(), aspectRatio({ contentMode: "fit" }), frame({ width: MARK_SIZE, height: MARK_SIZE })]} />;
}

export function useHarnessLogoUris(agents: readonly SpawnComposerOption[]) {
	const ids = useMemo(() => agents.map((agent) => agent.id), [agents]);
	const [uris, setUris] = useState<Record<string, string>>({});
	useEffect(() => {
		let cancelled = false;
		void Promise.all(ids.map(async (id) => {
			const source = logoFor(id);
			if (!source) return [id, ""] as const;
			const asset = Asset.fromModule(source);
			await asset.downloadAsync();
			return [id, asset.localUri ?? asset.uri] as const;
		})).then((entries) => {
			if (!cancelled) setUris(Object.fromEntries(entries.filter(([, uri]) => Boolean(uri))));
		});
		return () => { cancelled = true; };
	}, [ids]);
	return uris;
}
