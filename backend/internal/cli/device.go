package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// deviceCallTimeout outlasts the daemon's own limits for a device call (a
// cold boot, an install, a launch, a shutdown: up to 4 minutes), so the CLI
// never gives up first and cancels one halfway.
const deviceCallTimeout = 5 * time.Minute

// The CLI mirrors the daemon's device DTOs instead of importing controllers.
type devicePlatformDTO struct {
	Platform  string `json:"platform"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type deviceSummaryDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Version  string `json:"version,omitempty"`
	Booted   bool   `json:"booted"`
}

type deviceLeaseDTO struct {
	SessionID   string           `json:"sessionId"`
	Provider    string           `json:"provider"`
	Device      deviceSummaryDTO `json:"device"`
	Reason      string           `json:"reason"`
	AgentDevice struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"agentDevice"`
	OpenedAt time.Time `json:"openedAt"`
}

type deviceStatusDTO struct {
	Available bool                `json:"available"`
	Enabled   bool                `json:"enabled"`
	State     string              `json:"state"`
	Error     string              `json:"error,omitempty"`
	Platforms []devicePlatformDTO `json:"platforms"`
	Devices   []deviceSummaryDTO  `json:"devices"`
	Leases    []deviceLeaseDTO    `json:"leases"`
}

type sessionDeviceDTO struct {
	Lease *deviceLeaseDTO `json:"lease"`
}

func newDeviceCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Open and drive an iOS Simulator or Android Emulator for this AO session",
		Long: "Open an iOS Simulator or Android Emulator for the current AO session. The user\n" +
			"watches and taps it in the desktop Device tab and on the AO phone app.\n\n" +
			"Build the app with the project's own tools, then install it with `ao device install`\n" +
			"(or let `expo run:ios` / `react-native run-android` install it), and drive it with the\n" +
			"agent-device command that `ao device open` prints.",
		Args: noArgs,
	}
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "print the structured response as JSON")

	cmd.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Turn device support on: install the pinned device tools and start the device hub",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status deviceStatusDTO
			if err := ctx.postJSON(cmd.Context(), "devices/setup", nil, &status); err != nil {
				return err
			}
			return writeDeviceStatus(cmd.OutOrStdout(), status, jsonOutput)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show device support, platform availability, simulators, emulators and leases",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status deviceStatusDTO
			if err := ctx.getJSON(cmd.Context(), "devices", &status); err != nil {
				return err
			}
			if err := writeDeviceStatus(cmd.OutOrStdout(), status, jsonOutput); err != nil {
				return err
			}
			// Devices that could not be listed are not an empty machine.
			if status.Error != "" {
				return errors.New("device support reported an error")
			}
			return nil
		},
	})

	var platform, deviceID string
	open := &cobra.Command{
		Use:   "open",
		Short: "Pick, boot and lease a device to this session, and print how to drive it",
		Long: "Pick, boot and lease a simulator or emulator to this session. Without flags AO\n" +
			"picks the platform from the project and this machine. Reopening returns the\n" +
			"session's existing device.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if platform != "" && platform != "ios" && platform != "android" {
				return usageError{errors.New("--platform must be ios or android")}
			}
			body := map[string]string{}
			if platform != "" {
				body["platform"] = platform
			}
			if deviceID != "" {
				body["deviceId"] = deviceID
			}
			var out sessionDeviceDTO
			if err := ctx.sessionDeviceCall(cmd.Context(), "open", body, &out); err != nil {
				return err
			}
			return writeDeviceLease(cmd.OutOrStdout(), out, jsonOutput, true)
		},
	}
	open.Flags().StringVar(&platform, "platform", "", "ios or android (default: picked from the project and this machine)")
	open.Flags().StringVar(&deviceID, "device", "", "simulator UDID, emulator serial, or device name")
	cmd.AddCommand(open)

	cmd.AddCommand(&cobra.Command{
		Use:   "install <path>",
		Short: "Install a built .app (iOS) or .apk (Android) on this session's device",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return usageError{err}
			}
			var out sessionDeviceDTO
			if err := ctx.sessionDeviceCall(cmd.Context(), "install", map[string]string{"path": path}, &out); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s on %s\n", path, deviceName(out))
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "launch <app-id>",
		Short: "Launch an installed app by bundle id (iOS) or package name (Android)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out sessionDeviceDTO
			if err := ctx.sessionDeviceCall(cmd.Context(), "launch", map[string]string{"appId": args[0]}, &out); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Launched %s on %s\n", args[0], deviceName(out))
			return err
		},
	})

	var shutdown bool
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Release this session's device",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Say so rather than claim a release when there was nothing to release.
			var held sessionDeviceDTO
			if err := ctx.sessionDeviceGet(cmd.Context(), &held); err == nil && held.Lease == nil {
				return writeDeviceLease(cmd.OutOrStdout(), held, jsonOutput, false)
			}
			var out sessionDeviceDTO
			if err := ctx.sessionDeviceCall(cmd.Context(), "close", map[string]bool{"shutdown": shutdown}, &out); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Released this session's device")
			return err
		},
	}
	closeCmd.Flags().BoolVar(&shutdown, "shutdown", false, "also power the simulator or emulator off")
	cmd.AddCommand(closeCmd)
	return cmd
}

// sessionDeviceCall posts to this session's device route with its capability.
func (c *commandContext) sessionDeviceCall(ctx context.Context, action string, body, out any) error {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("ao device must run inside an AO session (AO_SESSION_ID is not set)")}
	}
	headers := map[string]string{}
	if capability := strings.TrimSpace(os.Getenv("AO_BROWSER_CAPABILITY")); capability != "" {
		headers[browserCapabilityHeader] = capability
	}
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/device/" + action
	return c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, path, body, out, headers, deviceCallTimeout)
}

// sessionDeviceGet reads this session's lease (null when it holds no device).
func (c *commandContext) sessionDeviceGet(ctx context.Context, out *sessionDeviceDTO) error {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("ao device must run inside an AO session (AO_SESSION_ID is not set)")}
	}
	headers := map[string]string{}
	if capability := strings.TrimSpace(os.Getenv("AO_BROWSER_CAPABILITY")); capability != "" {
		headers[browserCapabilityHeader] = capability
	}
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/device"
	return c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodGet, path, nil, out, headers, commandTimeout)
}

func deviceName(out sessionDeviceDTO) string {
	if out.Lease == nil {
		return "the device"
	}
	return out.Lease.Device.Name
}

func writeDeviceLease(w io.Writer, out sessionDeviceDTO, jsonOutput, howTo bool) error {
	if jsonOutput {
		return writeJSON(w, out)
	}
	if out.Lease == nil {
		_, err := fmt.Fprintln(w, "This session has no device.")
		return err
	}
	l := out.Lease
	version := ""
	if l.Device.Version != "" {
		version = " (" + l.Device.Version + ")"
	}
	if _, err := fmt.Fprintf(w, "Opened %s%s via %s: %s.\nThe user is watching it in the Device tab.\n", l.Device.Name, version, l.Provider, l.Reason); err != nil {
		return err
	}
	// Builds must aim at this device: other sessions' devices may be running.
	aim := "--device " + l.Device.ID + " or -destination id=" + l.Device.ID
	if l.Device.Platform == "android" {
		aim = "ANDROID_SERIAL=" + l.Device.ID + " or --deviceId " + l.Device.ID
	}
	if _, err := fmt.Fprintf(w, "Device id: %s (aim builds at it: %s).\n", l.Device.ID, aim); err != nil {
		return err
	}
	if !howTo || l.AgentDevice.Command == "" {
		return nil
	}
	flags := ""
	if len(l.AgentDevice.Args) > 0 {
		flags = " " + strings.Join(l.AgentDevice.Args, " ")
	}
	_, err := fmt.Fprintf(w, "\nDrive it with agent-device. This command is already bound to this session's device; run it with a subcommand and nothing else:\n"+
		"  %[1]s open <bundle-or-package-id>%[2]s\n"+
		"  %[1]s snapshot -i%[2]s\n"+
		"  %[1]s click @e3%[2]s\n"+
		"  %[1]s screenshot /tmp/device.png%[2]s\n"+
		"Run `%[1]s help workflow` for refs, waits and recovery.\n", l.AgentDevice.Command, flags)
	return err
}

func writeDeviceStatus(w io.Writer, s deviceStatusDTO, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(w, s)
	}
	state := s.State
	switch {
	case !s.Available:
		state = "unavailable (devices are a Developer mode preview: turn on Developer mode in Orchestrator's Settings > General)"
	case !s.Enabled:
		state = "off (run `ao device setup` to turn it on)"
	}
	if _, err := fmt.Fprintf(w, "Device support: %s\n", state); err != nil {
		return err
	}
	if s.Error != "" {
		if _, err := fmt.Fprintf(w, "Error: %s\n", s.Error); err != nil {
			return err
		}
	}
	for _, p := range s.Platforms {
		line := "available"
		if !p.Available {
			line = "unavailable: " + p.Reason
		}
		if _, err := fmt.Fprintf(w, "  %-8s %s\n", p.Platform, line); err != nil {
			return err
		}
	}
	if len(s.Devices) > 0 {
		if _, err := fmt.Fprintln(w, "Devices:"); err != nil {
			return err
		}
	}
	held := map[string]string{}
	for _, l := range s.Leases {
		held[l.Device.ID] = l.SessionID
	}
	for _, d := range s.Devices {
		state := "off"
		if d.Booted {
			state = "booted"
		}
		if owner := held[d.ID]; owner != "" {
			state += ", leased to " + owner
		}
		if _, err := fmt.Fprintf(w, "  %-8s %s %s [%s] %s\n", d.Platform, d.Name, d.Version, state, d.ID); err != nil {
			return err
		}
	}
	return nil
}
