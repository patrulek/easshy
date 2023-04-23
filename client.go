// easshy is a package that provides simple API over standard ssh package.
// This package allows to execute multiple commands in a single ssh session and read output of each command separately.
//
// Client supports only pubkey authentication method.
package easshy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/patrulek/stepper"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// final states
	s_new int32 = iota
	s_idle
	s_closed

	// transitive states
	s_starting
	s_executing
	s_closing
)

var (
	// silent terminal mode
	modes = ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	// ErrUnsupportedProtocol is return when trying to connect with non-ssh protocol scheme.
	ErrUnsupportedProtocol = errors.New("unsupported protocol")

	// ErrNoSession is returned when trying to execute command without shell session.
	ErrNoSession = errors.New("no session")

	// ErrExecuting is returned when trying to execute command, despite another one is currently executing.
	ErrExecuting = errors.New("executing")

	// ErrClosed is returned when trying to perform any operation on closed connection.
	ErrClosed = errors.New("connection closed")
)

// Client is a ssh.Client wrapper that allows for executing multiple command in a single session and read output of each command separately.
type Client struct {
	conn  *ssh.Client
	shell *shell
	state *stepper.Stepper
}

// Config represents data needed for connection to remote host.
type Config struct {
	URL            string // Remote host, should be in format: [scheme][username]@<host>[:port]
	KeyPath        string // Path to private key used for authentication.
	PassPath       string // Path to password for a given key. Should be empty if key does not require password.
	KnownHostsPath string // Path to known hosts file. Required to authenticate remote host if Insecure is false.
	Insecure       bool   // If true, remote host will be not validated.
}

// NewClient connects to remote host and returns *Shell object. It is required to call StartSession before trying to execute commands.
// Returns error if cannot connect to remote host or context has no or invalid deadline.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, fmt.Errorf("connection timeout not set")
	}
	if deadline.Before(time.Now()) {
		return nil, fmt.Errorf("connection timeout too low")
	}

	timeout := deadline.Sub(time.Now())

	url, err := parse(cfg.URL)
	if err != nil {
		return nil, err
	}

	signer, err := getSignerFromKeyfile(cfg.KeyPath, cfg.PassPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "couldnt get signer from key", err)
	}

	var hkcb ssh.HostKeyCallback
	if cfg.Insecure {
		hkcb = ssh.InsecureIgnoreHostKey()
	} else {
		hostKeyCallback, err := knownhosts.New(cfg.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", "could not create hostkeycallback function", err)
		}

		hkcb = hostKeyCallback
	}

	config := &ssh.ClientConfig{
		User: url.User.Username(),
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hkcb,
		Timeout:         timeout,
	}

	conn, err := ssh.Dial("tcp", url.Host, config)
	if err != nil {
		return nil, err
	}

	sh := &Client{
		conn:  conn,
		state: stepper.New(s_new),
	}

	return sh, nil
}

// parse inserts "ssh://" scheme if no other scheme found, then fallthrough to url.Parse function
func parse(URL string) (*url.URL, error) {
	before, _, found := strings.Cut(URL, "://")
	if found && before != "ssh" {
		return nil, ErrUnsupportedProtocol
	}

	if !found {
		URL = "ssh://" + URL
	}

	url, err := url.Parse(URL)
	if err != nil {
		return nil, err
	}

	if hostport := strings.Split(url.Host, ":"); len(hostport) == 1 {
		url.Host += ":22"
	}

	return url, nil
}

// getSignerFromKeyFile reads key and its password (if defined) from a given paths and create ssh.Signer for them.
func getSignerFromKeyfile(keyPath, passPath string) (ssh.Signer, error) {
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", "failed to read keyfile", err)
	}

	if passPath != "" {
		passBytes, err := os.ReadFile(passPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", "failed to read passfile", err)
		}

		return ssh.ParsePrivateKeyWithPassphrase(keyBytes, passBytes)
	}

	return ssh.ParsePrivateKey(keyBytes)
}

// StartSession initiates a new shell session for the current connection.
// Invalidates previous session, if such exist.
// Returns ErrClosed if the connection has already been closed.
func (this *Client) StartSession(ctx context.Context) error {
	startTransition := stepper.NewTransition(
		s_starting,
		s_idle,
		func(fctx context.Context) error {
			if err := this.ensureNoSession(); err != nil {
				return err
			}

			return this.startSession(fctx)
		},
		func(error) int32 {
			if this.shell == nil {
				return s_new
			}

			return s_idle
		},
	)

	return this.state.Step(ctx, map[int32]any{
		s_closed: ErrClosed,
		s_new:    startTransition,
		s_idle:   startTransition,
	})
}

// ensureNoSession invalidates and removes current session if it exist.
func (this *Client) ensureNoSession() (err error) {
	if this.shell == nil {
		return nil
	}

	err = this.shell.close()
	this.shell = nil

	return
}

// startSession creates new *sshSession object and set it to current connection.
// Session also initializes shell on remote host.
// It is needed to ensure that previous session was already closed before calling this method.
func (this *Client) startSession(ctx context.Context) error {
	session, err := this.conn.NewSession()
	if err != nil {
		return err
	}

	shell, err := newShell(session)
	if err != nil {
		return err
	}

	if err := shell.start(ctx); err != nil {
		ierr := shell.close()
		return errors.Join(err, ierr)
	}

	this.shell = shell
	return nil
}

// Execute attempts to execute the given command on the remote host, then waits for an output of the executed command.
// A call to StartSession must be made prior to using Execute, otherwise ErrNoSession will be returned.
// The connection must be active when calling this function, otherwise ErrClosed will be returned.
// If another command is currently executing, ErrExecuting will be returned.
func (this *Client) Execute(ctx context.Context, cmd string) (output string, err error) {
	executeTransition := stepper.NewTransition(
		s_executing,
		s_idle,
		func(fctx context.Context) error {
			if err := this.shell.write(cmd); err != nil {
				return err
			}

			output, err = this.shell.read(fctx)
			return err
		},
		func(error) int32 {
			return s_idle
		},
	)

	err = this.state.Step(ctx, map[int32]any{
		s_new:    ErrNoSession,
		s_closed: ErrClosed,
		s_idle:   executeTransition,
	})

	switch {
	case err == stepper.ErrBusy:
		err = ErrExecuting
		fallthrough
	case err != nil:
		return "", err
	default:
		return output, nil
	}
}

// Close waits for current command to end its execution and then immediately closes current connection.
// After this call all other calls will return ErrClosed.
func (this *Client) Close(ctx context.Context) error {
	closeTransition := stepper.NewTransition(
		s_closing,
		s_closed,
		func(context.Context) error {
			var errs []error

			errs = append(errs, this.ensureNoSession())
			errs = append(errs, this.conn.Close())

			this.conn, this.shell = nil, nil

			return errors.Join(errs...)
		},
		func(error) int32 {
			if this.shell == nil {
				return s_new
			}

			return s_idle
		},
	)

	return this.state.Queue(ctx, map[int32]any{
		s_closed: ErrClosed,
		s_new:    closeTransition,
		s_idle:   closeTransition,
	})
}
