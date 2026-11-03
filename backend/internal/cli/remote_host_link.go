package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"rsc.io/qr"
)

// The AuthKit client the desktop signs in with. It is public configuration.
// Release builds stamp the desktop's VITE_WORKOS_CLIENT_ID here (see
// frontend/scripts/build-daemon.mjs) so the CLI signs in to the same WorkOS
// environment, and so the same AO account, as the desktop it ships with.
// AO_WORKOS_CLIENT_ID overrides it at run time.
var defaultWorkOSClientID = "client_01KZ3VRKC374HS91XGRDPT3671"

// workOSAPIBase is a variable so tests can point it at a fake.
var workOSAPIBase = "https://api.workos.com"

// remoteHostLinkLabelMaxBytes matches AO Cloud's limit on host labels.
const remoteHostLinkLabelMaxBytes = 64

type remoteHostAccountToken struct {
	HostID  string `json:"hostId"`
	Token   string `json:"token"`
	HostKey string `json:"hostKey"`
}

type accountHostDirectory struct {
	Hosts []struct {
		HostID string `json:"hostId"`
		Label  string `json:"label"`
		Token  string `json:"token"`
	} `json:"hosts"`
	FixedAddresses bool `json:"fixedAddresses"`
	HostKeys       bool `json:"hostKeys"`
}

// errTunnelNotReady means the wait ran out before the daemon reported an
// address; the tunnel may still come up.
var errTunnelNotReady = errors.New("the tunnel address is not ready; run `ao remote-host status`")

type workOSDeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type workOSDeviceSession struct {
	AccessToken string `json:"access_token"`
	User        struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
	Error string `json:"error"`
}

// newRemoteHostLinkCommand links a machine with no desktop app, such as a VM,
// to an AO account the way a signed-in desktop links itself: the user approves
// a sign-in code on any device, and the machine then appears on all of the
// account's desktops and phones. The account session is used for this one
// registration and never stored; from then on the machine speaks to AO Cloud
// with its own credential.
func newRemoteHostLinkCommand(ctx *commandContext) *cobra.Command {
	var label string
	var noQR bool
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link this machine to your AO account by signing in from another device",
		Long: "Link this machine to your AO account by signing in from another device.\n\n" +
			"Open the printed link on any device, or scan its QR code with your phone, and sign in.\n" +
			"This machine then appears on every desktop and phone signed in to that account,\n" +
			"with no address or password to copy. Remote access is turned on if it is off.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runRemoteHostLink(cmd.Context(), cmd.OutOrStdout(), label, !noQR)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Name shown on your devices (default: the name your account has for it, else the hostname)")
	cmd.Flags().BoolVar(&noQR, "no-qr", false, "Do not print a QR code for the sign-in link")
	return cmd
}

func (c *commandContext) runRemoteHostLink(ctx context.Context, out io.Writer, label string, showQR bool) error {
	// The daemon's AO Cloud, not this shell's: its connector asks that one for
	// the machine's address.
	var settings struct {
		CloudControlPlaneURL string `json:"cloudControlPlaneUrl"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "settings", nil, &settings); err != nil {
		return remoteHostDaemonError(err)
	}
	cloudBase := strings.TrimRight(settings.CloudControlPlaneURL, "/")
	if cloudBase == "" {
		return errors.New("AO Cloud is not configured for this machine's AO")
	}

	// Nothing on this machine changes until the sign-in succeeds, so a
	// declined or abandoned sign-in leaves remote access as it was.
	session, err := c.signInWithDeviceCode(ctx, out, showQR)
	if err != nil {
		return err
	}
	var status remoteHostStatus
	if err := c.doJSON(ctx, http.MethodGet, "mobile/status", nil, &status); err != nil {
		return remoteHostDaemonError(err)
	}
	if !status.Enabled {
		// A machine with no screen is reached only through the tunnel, so its
		// listener stays on loopback.
		if err := c.doJSON(ctx, http.MethodPost, "mobile/enable-tunnel-only", nil, &status); err != nil {
			return remoteHostDaemonError(err)
		}
	}
	if status.Password == "" || status.HostID == "" {
		return errors.New("remote access did not start; run `ao remote-host status`")
	}
	// The account's devices reach the machine through its tunnel; this starts
	// one on a listener that was LAN-only, as `enable --tunnel` does.
	if err := c.doJSON(ctx, http.MethodPost, "mobile/remote-access", nil, &status); err != nil {
		return remoteHostDaemonError(err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var directory accountHostDirectory
	if err := cloudJSON(ctx, client, http.MethodGet, cloudBase+"/api/cloud/v1/me/hosts", session.AccessToken, nil, &directory); err != nil {
		return fmt.Errorf("read your AO account's machines: %w", err)
	}
	listedLabel, listedToken := "", ""
	for _, host := range directory.Hosts {
		if host.HostID == status.HostID {
			listedLabel, listedToken = host.Label, host.Token
		}
	}
	// The same account gets the token it already holds, so linking again is
	// safe; another account rotates it, taking the machine from the old one.
	var issued remoteHostAccountToken
	headers := map[string]string{"Authorization": "Bearer " + status.Password, "X-AO-Account-ID": session.User.ID}
	if err := c.doJSONPathWithHeaders(ctx, http.MethodPost, "/api/v1/remote-host/account-token", nil, &issued, headers); err != nil {
		return remoteHostDaemonError(err)
	}
	// Keep a name set on another device unless --label replaces it.
	if label = fitHostLabel(label); label == "" {
		label = listedLabel
	}
	if label == "" {
		hostname, _ := os.Hostname()
		label = fitHostLabel(hostname)
	}
	if label == "" {
		label = "Remote host"
	}
	registration := map[string]string{"label": label, "token": issued.Token}
	if directory.HostKeys && issued.HostKey != "" {
		registration["hostKey"] = issued.HostKey
	}
	if !directory.FixedAddresses {
		address, err := c.waitForTunnelAddress(ctx, false)
		if err != nil {
			return err
		}
		registration["url"] = address
	}
	put := cloudBase + "/api/cloud/v1/me/hosts/" + url.PathEscape(issued.HostID)
	// No time limit: AO Cloud may first make or retire the machine's tunnel,
	// which can take more than a minute, and finishes either way.
	putClient := &http.Client{}
	err = cloudJSON(ctx, putClient, http.MethodPut, put, session.AccessToken, registration, nil)
	var limited *cloudError
	if errors.As(err, &limited) && limited.status == http.StatusConflict && registration["url"] == "" {
		// Out of fixed addresses: list it at its quick tunnel instead,
		// which AO Cloud follows when the address changes.
		address, waitErr := c.waitForTunnelAddress(ctx, false)
		if waitErr != nil {
			return waitErr
		}
		registration["url"] = address
		err = cloudJSON(ctx, putClient, http.MethodPut, put, session.AccessToken, registration, nil)
	}
	if err != nil {
		return fmt.Errorf("add this machine to your AO account: %w; run `ao remote-host link` again", err)
	}
	message := fmt.Sprintf("Linked %q to %s.\n", label, session.User.Email)
	if listedToken == issued.Token && listedLabel == label {
		message = fmt.Sprintf("This machine is already linked to %s.\n", session.User.Email)
	}
	if _, err := io.WriteString(out, message); err != nil {
		return err
	}

	// The connector asks AO Cloud for this machine's address now that an
	// account holds it.
	if err := c.doJSON(ctx, http.MethodPost, "mobile/remote-access", nil, &status); err != nil {
		return remoteHostDaemonError(err)
	}
	address, err := c.waitForTunnelAddress(ctx, directory.FixedAddresses && registration["url"] == "")
	if errors.Is(err, errTunnelNotReady) {
		_, err = fmt.Fprintln(out, "The address is not ready yet; run `ao remote-host status` in a minute.")
		return err
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Address: %s\nYour desktops and phones signed in to %s list this machine within about 30 seconds.\n"+
		"Cloudflare terminates TLS and can see the connection password and traffic.\n", address, session.User.Email)
	return err
}

// waitForTunnelAddress returns the tunnel's https origin once the daemon
// reports one; with fixed, only once it is the fixed address.
func (c *commandContext) waitForTunnelAddress(ctx context.Context, fixed bool) (string, error) {
	deadline := c.deps.Now().Add(90 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var status remoteHostStatus
		if err := c.doJSON(ctx, http.MethodGet, "mobile/status", nil, &status); err != nil {
			return "", remoteHostDaemonError(err)
		}
		if !fixed || status.Tunnel.Fixed {
			for _, endpoint := range status.Endpoints {
				if endpoint.Kind == "tunnel" && endpoint.Secure {
					origin := "https://" + endpoint.Host
					if endpoint.Port != 443 && endpoint.Port != 0 {
						origin += ":" + strconv.Itoa(endpoint.Port)
					}
					return origin, nil
				}
			}
		}
		if !status.Tunnel.Supported && !status.Tunnel.Installing {
			return "", errors.New("tunnel unavailable: install cloudflared, then rerun `ao remote-host link`")
		}
		if !c.deps.Now().Before(deadline) {
			return "", errTunnelNotReady
		}
		c.deps.Sleep(2 * time.Second)
	}
}

// signInWithDeviceCode runs WorkOS's device authorization flow: the user
// approves the printed code on any signed-in browser or phone.
func (c *commandContext) signInWithDeviceCode(ctx context.Context, out io.Writer, showQR bool) (workOSDeviceSession, error) {
	clientID := strings.TrimSpace(os.Getenv("AO_WORKOS_CLIENT_ID"))
	if clientID == "" {
		clientID = defaultWorkOSClientID
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var device workOSDeviceAuthorization
	body, _ := json.Marshal(map[string]string{"client_id": clientID})
	if err := workOSPost(ctx, client, "/user_management/authorize/device", "application/json", bytes.NewReader(body), &device); err != nil {
		return workOSDeviceSession{}, fmt.Errorf("start sign-in: %w", err)
	}
	link := device.VerificationURIComplete
	if link == "" {
		link = device.VerificationURI
	}
	if _, err := fmt.Fprintf(out, "\nTo link this machine to your AO account, open this link on any device and sign in:\n\n  %s\n\n", link); err != nil {
		return workOSDeviceSession{}, err
	}
	if showQR {
		if err := writeQR(out, link); err != nil {
			return workOSDeviceSession{}, err
		}
		if _, err := fmt.Fprintln(out, "Or scan this code with your phone's camera."); err != nil {
			return workOSDeviceSession{}, err
		}
	}
	if _, err := fmt.Fprintf(out, "Check that the page shows the code %s.\n\nWaiting for you to sign in...\n", device.UserCode); err != nil {
		return workOSDeviceSession{}, err
	}

	interval := 5 * time.Second
	if device.Interval > 0 {
		interval = time.Duration(device.Interval) * time.Second
	}
	expires := c.deps.Now().Add(time.Duration(max(device.ExpiresIn, 60)) * time.Second)
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {device.DeviceCode},
		"client_id":   {clientID},
	}.Encode()
	for {
		c.deps.Sleep(interval)
		if err := ctx.Err(); err != nil {
			return workOSDeviceSession{}, err
		}
		var session workOSDeviceSession
		err := workOSPost(ctx, client, "/user_management/authenticate", "application/x-www-form-urlencoded", strings.NewReader(form), &session)
		switch {
		case err == nil && session.AccessToken != "":
			if _, err := fmt.Fprintf(out, "Signed in as %s.\n", session.User.Email); err != nil {
				return workOSDeviceSession{}, err
			}
			return session, nil
		case session.Error == "authorization_pending":
		case session.Error == "slow_down":
			interval += 5 * time.Second
		case session.Error == "access_denied":
			return workOSDeviceSession{}, errors.New("sign-in was declined")
		case session.Error == "expired_token":
			return workOSDeviceSession{}, errors.New("the sign-in link expired; run `ao remote-host link` again")
		case err != nil && session.Error == "":
			// A dropped connection is retried until the code expires.
		default:
			return workOSDeviceSession{}, fmt.Errorf("sign-in failed: %s", session.Error)
		}
		if !c.deps.Now().Before(expires) {
			return workOSDeviceSession{}, errors.New("the sign-in link expired; run `ao remote-host link` again")
		}
	}
}

// workOSPost decodes the body into out for any status, so a 400 still yields
// its OAuth error field.
func workOSPost(ctx context.Context, client *http.Client, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, workOSAPIBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil && res.StatusCode < 300 {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("WorkOS answered %d", res.StatusCode)
	}
	return nil
}

type cloudError struct {
	status int
	code   string
}

func (e *cloudError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("AO Cloud answered %d (%s)", e.status, e.code)
	}
	return fmt.Sprintf("AO Cloud answered %d", e.status)
}

func cloudJSON(ctx context.Context, client *http.Client, method, target, bearer string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var envelope struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &envelope)
		return &cloudError{status: res.StatusCode, code: envelope.Code}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func fitHostLabel(name string) string {
	runes := []rune(strings.TrimSpace(name))
	for len(string(runes)) > remoteHostLinkLabelMaxBytes {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes))
}

// writeQR prints text as a QR code in black on white with half-block
// characters, two modules per character row, so it scans on dark and light
// terminals alike.
func writeQR(out io.Writer, text string) error {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return err
	}
	const quiet = 4 // the QR spec's minimum margin
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	size := code.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		b.WriteString("  \x1b[30;47m")
		for x := 0; x < size; x++ {
			top, bottom := black(x, y), black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	_, err = io.WriteString(out, b.String())
	return err
}
