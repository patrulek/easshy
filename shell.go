package easshy

import (
	"bytes"
	"context"
	"errors"
	"io"

	"golang.org/x/crypto/ssh"
)

var (
	// ErrBufferOverflow is returned when command output is greater than buffer.
	ErrBufferOverflow = errors.New("buffer overflow")

	// ErrUnsupportedPrompt is returned when remote shell's prompt doesn't end with space.
	ErrUnsupportedPrompt = errors.New("unsupported prompt")
)

// shell is a wrapper over ssh.Session that allows to call multiple commands and read their output separately, preserving shell context.
type shell struct {
	*ssh.Session
	stdin  io.WriteCloser // Standard input pipe.
	stdout io.Reader      // Standard output pipe.

	promptSuffix string // Shell's default prompt parsed from `/bin/sh`.
	readBuffer   []byte // Read buffer for command output. Cant be greater than 1MB.
}

// newShell wraps over *ssh.Session to create *shell object.
func newShell(session *ssh.Session) (*shell, error) {
	if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
		ierr := session.Close()
		return nil, errors.Join(err, ierr)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		ierr := session.Close()
		return nil, errors.Join(err, ierr)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		ierr := session.Close()
		return nil, errors.Join(err, ierr)
	}

	sshSession := &shell{
		Session:    session,
		stdin:      stdin,
		stdout:     stdout,
		readBuffer: make([]byte, 32*1024),
	}

	return sshSession, nil
}

// start starts the remote shell and sets proper shell's prompt suffix.
func (this *shell) start(ctx context.Context) error {
	if err := this.Session.Start("/bin/sh"); err != nil {
		return err
	}

	// wait for prompt to show; we will save that prompt suffix for later usage

	var buf [256]byte // should be sufficient for shell initial prompt

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		n, err := this.stdout.Read(buf[:])
		if err != nil {
			return err
		}

		if n < 2 || buf[n-1] != ' ' {
			return ErrUnsupportedPrompt
		}

		this.promptSuffix = string(buf[n-2 : n])
	}

	return nil
}

// runOpts apply all functional options to shell.
func (this *shell) runOpts(ctx context.Context, opts ...Option) error {
	for _, opt := range opts {
		if err := opt(ctx, this); err != nil {
			return err
		}
	}

	return nil
}

// setContext set given shell context to current session.
func (this *shell) setContext(ctx context.Context, shCtx ShellContext) error {
	opt := WithShellContext(shCtx)
	return opt(ctx, this)
}

func (this *shell) write(cmd string) error {
	_, err := this.stdin.Write([]byte(cmd + "\n"))
	return err
}

func (this *shell) read(ctx context.Context) (string, error) {
	// read until we reach shell's prompt or cancel
	for t := 0; t < len(this.readBuffer); {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			n, err := this.stdout.Read(this.readBuffer[t:])
			if err != nil {
				return "", err
			}

			t += n
			if err := this.growBuffer(); err != nil {
				break
			}

			if string(this.readBuffer[t-2:t]) == this.promptSuffix {
				// remove whole prompt, but keep new line
				t = bytes.LastIndex(this.readBuffer[:t-2], []byte("\n")) + 1
				return string(this.readBuffer[:t]), nil
			}
		}
	}

	return string(this.readBuffer), ErrBufferOverflow
}

// growBuffer will grow read buffer if its not sufficient to hold command output up to 1 MB.
func (this *shell) growBuffer() error {
	// no need to grow yet
	if len(this.readBuffer) < cap(this.readBuffer) {
		return nil
	}

	// cannot grow more
	if len(this.readBuffer) >= 1024*1024 {
		return ErrBufferOverflow
	}

	buf := make([]byte, len(this.readBuffer), 2*cap(this.readBuffer))
	copy(buf, this.readBuffer)
	this.readBuffer = buf

	return nil
}

// close sends `exit` command to current shell's session and then closes ssh.Session.
// Ignores all io.EOF errors.
func (this *shell) close() error {
	var errs []error

	errs = append(errs, this.write("exit"))
	errs = append(errs, this.Session.Wait())
	errs = append(errs, this.stdin.Close())
	errs = append(errs, this.Session.Close())

	for i := 0; i < len(errs); i++ {
		if errs[i] == io.EOF {
			errs[i] = nil
		}
	}

	this.Session, this.stdin, this.stdout = nil, nil, nil

	return errors.Join(errs...)
}
