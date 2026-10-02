package runner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
)

const (
	codexDeviceVerificationURL = "https://auth.openai.com/codex/device"
	codexDeviceCodePrefix      = "Codex device code:"
	deviceResultPrefix         = "AO-DEVICE-RESULT-V1:"
	deviceFrameLimit           = 2 << 20
)

var errDeviceLogin = errors.New("device login unavailable")

func runCodexDeviceLogin(ctx context.Context, stateDir string, output io.Writer) error {
	previousOutput := log.StandardLogger().Out
	log.SetOutput(io.Discard)
	defer log.SetOutput(previousOutput)
	state, err := LoadState(stateDir)
	if err != nil {
		return errDeviceLogin
	}
	return deviceLoginResult(ctx, state.Config, sdkauth.NewCodexAuthenticator(), os.Stdin, output)
}

func deviceLoginResult(ctx context.Context, cfg *sdkconfig.Config, authenticator sdkauth.Authenticator, input io.Reader, output io.Writer) error {
	key := make([]byte, 32)
	defer clear(key)
	if _, err := io.ReadFull(input, key); err != nil {
		return errDeviceLogin
	}
	auth, err := authenticator.Login(ctx, cfg, &sdkauth.LoginOptions{
		NoBrowser: true, Metadata: map[string]string{"codex_login_mode": "device"},
	})
	if err != nil || ctx.Err() != nil {
		return errDeviceLogin
	}
	sealed, err := sealDeviceCredential(key, auth)
	if err != nil {
		return errDeviceLogin
	}
	if _, err := fmt.Fprintln(output, deviceResultPrefix+sealed); err != nil {
		return errDeviceLogin
	}
	return nil
}

func newCodexDeviceProcessStarter(stateDir string) func(context.Context) (codexDeviceLogin, error) {
	return func(ctx context.Context) (codexDeviceLogin, error) {
		executable, err := os.Executable()
		if err != nil {
			return codexDeviceLogin{}, errDeviceLogin
		}
		return startDeviceProcess(ctx, func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, executable, "_codex-device-login", "--state-dir", stateDir) // #nosec G702 -- Self executable and fixed argument vector; no shell interpretation.
		}, 30*time.Second)
	}
}

func startDeviceProcess(ctx context.Context, commandFor func(context.Context) *exec.Cmd, startupTimeout time.Duration) (codexDeviceLogin, error) {
	workerContext, cancel := context.WithCancel(ctx)
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		cancel()
		return codexDeviceLogin{}, errDeviceLogin
	}
	command := commandFor(workerContext)
	command.Stdin = bytes.NewReader(key)
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		clear(key)
		return codexDeviceLogin{}, errDeviceLogin
	}
	if err := command.Start(); err != nil {
		cancel()
		clear(key)
		_ = stdout.Close()
		return codexDeviceLogin{}, errDeviceLogin
	}
	code, done := make(chan string, 1), make(chan error, 1)
	result := make(chan *coreauth.Auth, 1)
	go func() {
		defer cancel()
		defer clear(key)
		defer close(done)
		defer close(result)
		var auth *coreauth.Auth
		var invalid bool
		codeSeen, total := false, 0
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), deviceFrameLimit)
		for scanner.Scan() {
			line := scanner.Text()
			total += len(line)
			if total > 2*deviceFrameLimit {
				invalid = true
				break
			}
			switch {
			case strings.HasPrefix(line, codexDeviceCodePrefix):
				value := strings.TrimSpace(strings.TrimPrefix(line, codexDeviceCodePrefix))
				if codeSeen || !validDeviceCode(value) {
					invalid = true
					break
				}
				codeSeen = true
				code <- value
			case strings.HasPrefix(line, deviceResultPrefix):
				if auth != nil {
					invalid = true
					break
				}
				var decodeErr error
				auth, decodeErr = openDeviceCredential(key, strings.TrimPrefix(line, deviceResultPrefix))
				invalid = decodeErr != nil
			}
			if invalid {
				break
			}
		}
		if invalid || scanner.Err() != nil {
			cancel()
		}
		waitErr := command.Wait()
		if invalid || scanner.Err() != nil || waitErr != nil || auth == nil || !codeSeen || ctx.Err() != nil {
			done <- errDeviceLogin
			return
		}
		result <- auth
		done <- nil
	}()
	stop := func() {
		cancel()
		_ = stdout.Close()
		<-done
	}
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	login := func(userCode string) codexDeviceLogin {
		return codexDeviceLogin{AuthorizationURL: codexDeviceVerificationURL, UserCode: userCode, Done: done, Result: result, close: stop}
	}
	select {
	case userCode := <-code:
		return login(userCode), nil
	case err := <-done:
		cancel()
		select {
		case userCode := <-code:
			if err == nil {
				value := login(userCode)
				value.Done = closedDeviceResult(nil)
				return value, nil
			}
		default:
		}
		return codexDeviceLogin{}, errDeviceLogin
	case <-ctx.Done():
		stop()
		return codexDeviceLogin{}, errDeviceLogin
	case <-timer.C:
		stop()
		return codexDeviceLogin{}, errDeviceLogin
	}
}

func validDeviceCode(value string) bool {
	return len(value) >= 4 && len(value) <= 64 && strings.IndexFunc(value, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
	}) < 0
}

func closedDeviceResult(err error) <-chan error {
	done := make(chan error, 1)
	done <- err
	close(done)
	return done
}
