package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type remoteHostPairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type remoteHostDevice struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	DeviceName string    `json:"deviceName"`
	Platform   string    `json:"platform"`
	Live       bool      `json:"live"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

func printPairingCode(cmd *cobra.Command, pairing remoteHostPairingCode) error {
	minutes := int(time.Until(pairing.ExpiresAt).Round(time.Minute).Minutes())
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"Pairing code: %s\nOne device can use it, within %d minutes. Run `ao remote-host pair` for another.\n",
		pairing.Code, max(minutes, 1))
	return err
}

func newRemoteHostPairCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use: "pair", Args: noArgs,
		Short: "Print a one-time code to pair a new device with this machine",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status remoteHostStatus
			if err := ctx.doJSON(cmd.Context(), http.MethodGet, "mobile/status", nil, &status); err != nil {
				return remoteHostDaemonError(err)
			}
			if !status.Enabled {
				return errors.New("remote access is off; run `ao remote-host enable` first")
			}
			var pairing remoteHostPairingCode
			if err := ctx.doJSON(cmd.Context(), http.MethodPost, "mobile/pairing-code", nil, &pairing); err != nil {
				return remoteHostDaemonError(err)
			}
			for _, endpoint := range status.Endpoints {
				scheme := "http"
				if endpoint.Secure {
					scheme = "https"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Address: %s://%s\n", scheme, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))); err != nil {
					return err
				}
			}
			return printPairingCode(cmd, pairing)
		},
	}
}

func listRemoteHostDevices(ctx *commandContext, cmd *cobra.Command) ([]remoteHostDevice, error) {
	var listing struct {
		Devices []remoteHostDevice `json:"devices"`
	}
	if err := ctx.doJSON(cmd.Context(), http.MethodGet, "mobile/devices", nil, &listing); err != nil {
		return nil, remoteHostDaemonError(err)
	}
	return listing.Devices, nil
}

func remoteHostDeviceName(device remoteHostDevice) string {
	switch {
	case device.Kind == "account":
		return "Account devices"
	case device.DeviceName != "":
		return device.DeviceName
	default:
		return "Unnamed device"
	}
}

func newRemoteHostDevicesCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use: "devices", Args: noArgs,
		Short: "List the devices that can reach this machine",
		RunE: func(cmd *cobra.Command, _ []string) error {
			devices, err := listRemoteHostDevices(ctx, cmd)
			if err != nil {
				return err
			}
			if len(devices) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No devices paired.")
				return err
			}
			access := map[string]string{
				"device":   "own token",
				"password": "pairing password",
				"account":  "account token (shared)",
			}
			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "ID\tNAME\tACCESS\tLAST SEEN"); err != nil {
				return err
			}
			for _, device := range devices {
				seen := "never"
				switch {
				case device.Live:
					seen = "connected"
				case !device.LastSeenAt.IsZero():
					seen = device.LastSeenAt.Local().Format("2006-01-02 15:04")
				}
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", device.ID, remoteHostDeviceName(device), access[device.Kind], seen); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	var all bool
	revoke := &cobra.Command{
		Use:   "revoke [ID]",
		Short: "Revoke one device, or every device with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return errors.New("give one device ID, or --all")
			}
			if all {
				var status remoteHostStatus
				if err := ctx.doJSON(cmd.Context(), http.MethodPost, "mobile/regenerate", nil, &status); err != nil {
					return remoteHostDaemonError(err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Revoked every device and unlinked the account. Pair again with `ao remote-host pair`, or `ao remote-host link` for the account.")
				return err
			}
			devices, err := listRemoteHostDevices(ctx, cmd)
			if err != nil {
				return err
			}
			var target *remoteHostDevice
			for i := range devices {
				if devices[i].ID == args[0] {
					target = &devices[i]
				}
			}
			if target == nil {
				return fmt.Errorf("no device with ID %q; run `ao remote-host devices`", args[0])
			}
			if err := ctx.doJSON(cmd.Context(), http.MethodDelete, "mobile/devices/"+url.PathEscape(target.ID), nil, nil); err != nil {
				return remoteHostDaemonError(err)
			}
			message := fmt.Sprintf("Revoked %s.", remoteHostDeviceName(*target))
			switch target.Kind {
			case "account":
				message = "Unlinked the account: its devices can no longer reach this machine. Run `ao remote-host link` to link it again."
			case "password":
				message = fmt.Sprintf("Removed %s from notifications. It still holds the pairing password; run `ao remote-host require-device-credentials on` to stop accepting that password.", remoteHostDeviceName(*target))
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
			return err
		},
	}
	revoke.Flags().BoolVar(&all, "all", false, "Revoke every device and unlink the account")
	cmd.AddCommand(revoke)
	return cmd
}

func newRemoteHostRequireDeviceCredentialsCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:       "require-device-credentials on|off",
		Short:     "Stop accepting the pairing password, so each device needs its own token",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var status remoteHostStatus
			body := map[string]bool{"enabled": args[0] == "on"}
			if err := ctx.doJSON(cmd.Context(), http.MethodPost, "mobile/require-device-credentials", body, &status); err != nil {
				return remoteHostDaemonError(err)
			}
			message := "The pairing password is accepted again."
			if status.RequireDeviceCredentials {
				message = "The pairing password is no longer accepted. A device needs its own token or the linked account's. Pair a device with `ao remote-host pair`."
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
			return err
		},
	}
}
