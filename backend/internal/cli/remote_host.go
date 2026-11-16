package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// remoteHostStatus mirrors the small part of the loopback mobile status API
// needed to pair another client with a headless daemon.
type remoteHostStatus struct {
	Enabled       bool `json:"enabled"`
	LoopbackOnly  bool `json:"loopbackOnly"`
	AccountLinked bool `json:"accountLinked"`
	// AccountLinkRefused: AO Cloud refused the machine's address under its
	// account token, so the account's devices may not list it.
	AccountLinkRefused       bool       `json:"accountLinkRefused"`
	HostID                   string     `json:"hostId"`
	Password                 string     `json:"password"`
	RequireDeviceCredentials bool       `json:"requireDeviceCredentials"`
	PasswordLastUsedAt       *time.Time `json:"passwordLastUsedAt"`
	Tunnel                   struct {
		Supported  bool   `json:"supported"`
		Installing bool   `json:"installing"`
		Running    bool   `json:"running"`
		Fixed      bool   `json:"fixed"`
		LastError  string `json:"lastError"`
	} `json:"tunnel"`
	Endpoints []struct {
		Kind   string `json:"kind"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Secure bool   `json:"secure"`
	} `json:"endpoints"`
}

func newRemoteHostCommand(ctx *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "remote-host", Short: "Manage this machine's remote listener"}
	var tunnel, tunnelOnly, showPassword bool
	for _, action := range []struct {
		name, method, path string
	}{
		{"status", "GET", "mobile/status"},
		{"enable", "POST", "mobile/enable-lan-only"},
		{"disable", "POST", "mobile/disable"},
	} {
		cmd := &cobra.Command{
			Use: action.name, Args: noArgs,
			Short: action.name + " this machine's authenticated remote listener",
			RunE: func(cmd *cobra.Command, _ []string) error {
				var status remoteHostStatus
				method, path := action.method, action.path
				if action.name == "enable" && tunnelOnly {
					method, path = "POST", "mobile/enable-tunnel-only"
				} else if action.name == "enable" && tunnel {
					if err := ctx.doJSON(cmd.Context(), "GET", "mobile/status", nil, &status); err != nil {
						return remoteHostDaemonError(err)
					}
					if status.Enabled && status.LoopbackOnly {
						return errors.New("listener is loopback-only; run `ao remote-host disable` before enabling LAN and tunnel access")
					}
					if !status.Enabled {
						if err := ctx.doJSON(cmd.Context(), "POST", "mobile/enable-lan-only", nil, &status); err != nil {
							return remoteHostDaemonError(err)
						}
					}
					method, path = "POST", "mobile/remote-access"
				}
				err := ctx.doJSON(cmd.Context(), method, path, nil, &status)
				if err != nil {
					return remoteHostDaemonError(err)
				}
				if !status.Enabled {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "Remote host disabled")
					return err
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Remote host enabled\nHost ID: %s\n", status.HostID); err != nil {
					return err
				}
				if status.AccountLinked {
					message := "Linked to an Orchestrator account: its signed-in devices list this machine."
					if status.AccountLinkRefused {
						message = "Orchestrator Cloud refuses this machine's account link, so your devices may not list it. Run `ao remote-host link` again."
					}
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
						return err
					}
				}
				publicAddress := false
				for _, endpoint := range status.Endpoints {
					scheme := "http"
					if endpoint.Secure {
						scheme = "https"
					}
					if endpoint.Kind == "tunnel" {
						publicAddress = true
					}
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Address: %s://%s\n", scheme, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))); err != nil {
						return err
					}
				}
				if publicAddress {
					message := "Fixed address: it stays the same after restarts."
					if !status.Tunnel.Fixed {
						message = "Quick tunnel: the address changes on restart and has no uptime guarantee."
						if !status.AccountLinked || status.AccountLinkRefused {
							message += " Run `ao remote-host link` to add this machine to your Orchestrator account, which gives it a fixed address when the account has one free."
						}
					}
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
						return err
					}
				}
				if (tunnel || tunnelOnly) && action.name == "enable" {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Cloudflare terminates TLS and can see the connection password and traffic."); err != nil {
						return err
					}
				}
				if !publicAddress && ((tunnel || tunnelOnly) && action.name == "enable" || status.LoopbackOnly || status.Tunnel.Running || status.Tunnel.LastError != "" || status.Tunnel.Installing) {
					message := "Tunnel: starting; run `ao remote-host status` for the HTTPS address"
					if !status.Tunnel.Supported {
						flag := "--tunnel"
						if status.LoopbackOnly || tunnelOnly {
							flag = "--tunnel-only"
						}
						message = "Tunnel unavailable: install cloudflared, then rerun `ao remote-host enable " + flag + "`"
						// AO downloads its own cloudflared where it has a pinned
						// build; the tunnel starts by itself when it lands.
						if status.Tunnel.Installing {
							message = "Tunnel: downloading cloudflared; run `ao remote-host status` for the HTTPS address"
						}
					} else if status.Tunnel.LastError != "" && !status.Tunnel.Running {
						message = "Tunnel unavailable: " + status.Tunnel.LastError
					}
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
						return err
					}
				}
				if status.RequireDeviceCredentials {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "The pairing password is off. Run `ao remote-host pair` for a code to pair a device.")
					return err
				}
				if action.name == "enable" {
					// Only a daemon without pairing codes prints the password.
					var pairing remoteHostPairingCode
					err := ctx.doJSON(cmd.Context(), http.MethodPost, "mobile/pairing-code", nil, &pairing)
					var apiErr apiResponseError
					unsupported := errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusNotImplemented)
					switch {
					case unsupported:
					case err != nil:
						return remoteHostDaemonError(err)
					case pairing.Code == "":
						return errors.New("the daemon returned an empty pairing code; run `ao remote-host pair`")
					default:
						return printPairingCode(cmd, pairing)
					}
				}
				if status.Password != "" && action.name == "status" && !showPassword {
					if status.PasswordLastUsedAt != nil {
						if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pairing password last used: %s\n", status.PasswordLastUsedAt.Local().Format("2006-01-02 15:04")); err != nil {
							return err
						}
					}
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "Pairing password hidden; run `ao remote-host status --show-password` to show it, or `ao remote-host pair` for a one-time code.")
					return err
				}
				if status.Password != "" {
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "Password: %s\n", status.Password)
					return err
				}
				return nil
			},
		}
		if action.name == "enable" {
			cmd.Flags().BoolVar(&tunnel, "tunnel", false, "Start Orchestrator's managed Cloudflare tunnel for access beyond the LAN")
			cmd.Flags().BoolVar(&tunnelOnly, "tunnel-only", false, "Bind only to loopback and start Orchestrator's managed Cloudflare tunnel")
			cmd.MarkFlagsMutuallyExclusive("tunnel", "tunnel-only")
		}
		if action.name == "status" {
			cmd.Flags().BoolVar(&showPassword, "show-password", false, "Print the pairing password")
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(newRemoteHostLinkCommand(ctx))
	root.AddCommand(newRemoteHostPairCommand(ctx))
	root.AddCommand(newRemoteHostDevicesCommand(ctx))
	root.AddCommand(newRemoteHostRequireDeviceCredentialsCommand(ctx))
	return root
}

func remoteHostDaemonError(err error) error {
	if errors.Is(err, errDaemonNotRunning) {
		return daemonUnavailableError{message: "Orchestrator daemon is not running — run `ao daemon` on this machine", cause: errDaemonNotRunning}
	}
	return err
}
