package easshy

import (
	"context"
	"errors"
	"io"
	"sync"
)

// ICmd represents command interface that can be executed in a batch call.
type ICmd interface {
	String() string
	IgnoreError() bool
	Ctx() ShellContext
}

// Serial is a convenience wrapper that establishes a single shell session on a given host and executes the provided commands sequentially.
// The function returns when the last command finishes execution or when an error occurs.
// If no error is returned, the result will contain separate output for each of the provided commands.
// In case of an error, the result will include separate output for all successfully executed commands prior to the error.
func Serial(ctx context.Context, cfg Config, cmds []ICmd, opts ...Option) ([]string, error) {
	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	defer client.Close(ctx)

	if err := client.StartSession(ctx, opts...); err != nil {
		return nil, err
	}

	result := make([]string, 0)
	for _, cmd := range cmds {
		output, err := client.Execute(ctx, cmd.String())
		if err != nil {
			if contextError(err) || !cmd.IgnoreError() {
				return result, err
			}
		}

		result = append(result, output)
	}

	return result, nil
}

// Stream is a convenience wrapper that establishes a single shell session on a given host and executes the provided commands sequentially.
// The function returns a *StreamReader that allows reading the output of the commands as soon as they finish execution.
// If no error occurs during command execution, the Reader will return separate outputs for all provided commands, and will return EOF when attempting to read more outputs.
// In case of an command error, the Reader will return that error instead.
func Stream(ctx context.Context, cfg Config, cmds []ICmd, opts ...Option) (*StreamReader, error) {
	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := client.StartSession(ctx, opts...); err != nil {
		return nil, err
	}

	bufC := make(chan string)
	errC := make(chan error)

	go stream(ctx, client, cmds, bufC, errC)
	return newStreamReader(bufC, errC), nil
}

// stream executes all commands one-by-one and sends the output to provided channels.
// Stops further execution on any error unless OptionalCmd was executed.
func stream(ctx context.Context, client *Client, cmds []ICmd, bufC chan<- string, errC chan<- error) {
	var wg sync.WaitGroup
	wg.Add(2)

	ierrC := make(chan error, 1)
	ibufC := make(chan string, len(cmds))

	// producer that executes commands and signals consumer about new output
	go func() {
		defer wg.Done()
		defer client.Close(ctx)
		defer close(ibufC)
		defer close(ierrC)

		for _, cmd := range cmds {
			output, err := client.Execute(ctx, cmd.String())
			sendErr := contextError(err) || (err != nil && !cmd.IgnoreError())

			if sendErr {
				ierrC <- err
				return
			}

			ibufC <- output
		}

		ierrC <- io.EOF
	}()

	// consumer that acts like a buffer in a case reader doesn't read output fast enough to not stall command execution
	go func() {
		defer wg.Done()
		defer close(errC)
		defer close(bufC)

		for output := range ibufC {
			select {
			case bufC <- output:
			case <-ctx.Done():
				return
			}
		}

		err := <-ierrC
		if contextError(err) {
			err = io.ErrClosedPipe
		}

		select {
		case errC <- err:
		case <-ctx.Done():
		}
	}()

	wg.Wait()
}

// StreamReader allows for reading commands output from a Stream call as soon as output arrives.
type StreamReader struct {
	err  error
	bufC <-chan string
	errC <-chan error
}

// newStreamReader creates new *StreamReader object that will wait on given channels to read from.
func newStreamReader(bufC <-chan string, errC <-chan error) *StreamReader {
	return &StreamReader{
		bufC: bufC,
		errC: errC,
	}
}

// Read waits for data from the Stream call.
// When the stream finishes execution (the last command returns), all subsequent calls to Read will return io.EOF.
// If any command in the stream fails in the middle of the stream, the given error will be returned for all subsequent calls.
// When Read function is called after stream's context cancels and before reading any error from stream, all subsequent calls to Read will return io.ErrClosedPipe.
func (this *StreamReader) Read() (output string, err error) {
	if this.err != nil {
		return "", this.err
	}

	select {
	case output, open := <-this.bufC:
		if !open {
			this.err = io.ErrClosedPipe
			return "", io.ErrClosedPipe
		}

		return output, nil
	case err, open := <-this.errC:
		if !open {
			this.err = io.ErrClosedPipe
			return "", io.ErrClosedPipe
		}

		this.err = err
		return "", err
	}
}

// Parallel is a convenience wrapper that establishes multiple shell sessions on a given host and executes each command in a separate session concurrently.
// The function returns when all commands finish execution or when any error occurs.
// If no error is returned, the result will contain separate output for each of the provided commands.
// In case of an error, the result will include separate output for all successfully executed commands, and an empty output for the ones that did not complete their execution.
func Parallel(ctx context.Context, cfg Config, cmds []ICmd, opts ...Option) ([]string, error) {
	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	defer client.Close(ctx)

	var mu sync.Mutex
	count := 0
	doneC := make(chan struct{})
	errC := make(chan error)
	result := make([]string, len(cmds))

	for i, cmd := range cmds {
		go func(i int, cmd ICmd) {
			output, err := client.runSession(ctx, cmd, opts...)

			select {
			case <-doneC:
				return
			default:
				if err != nil {
					if contextError(err) || !cmd.IgnoreError() {
						errC <- err
						return
					}
				}
			}

			mu.Lock()
			result[i] = output
			count++
			mu.Unlock()

			if count == len(cmds) {
				close(doneC)
			}
		}(i, cmd)
	}

	select {
	case err = <-errC:
		return result, err
	case <-ctx.Done():
		return result, ctx.Err()
	case <-doneC:
		return result, nil
	}
}

func contextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Cmd is a command which error, if occured, will not be ignored in a batch call.
type Cmd string

func (this Cmd) String() string {
	return string(this)
}

func (this Cmd) IgnoreError() bool {
	return false
}

func (this Cmd) Ctx() ShellContext {
	return ShellContext{}
}

// OptionalCmd is a command which error, if occured, will be ignored to not stop whole batch call.
// Will not ignore context errors.
type OptionalCmd string

func (this OptionalCmd) String() string {
	return string(this)
}

func (this OptionalCmd) IgnoreError() bool {
	return true
}

func (this OptionalCmd) Ctx() ShellContext {
	return ShellContext{}
}

// ContextCmd is a command that let you specify shell context that will be set before calling actual command.
// Errors returned during context setting will not be ignored regardless of IgnoreError result.
type ContextCmd struct {
	Cmd      string
	Optional bool
	Path     string
	Env      map[string]string
}

func (this ContextCmd) String() string {
	return this.Cmd
}

func (this ContextCmd) IgnoreError() bool {
	return this.Optional
}

func (this ContextCmd) Ctx() ShellContext {
	return ShellContext{
		Path: this.Path,
		Env:  this.Env,
	}
}

// ShellContext represents context that will be used to call a command.
type ShellContext struct {
	Path string
	Env  map[string]string
}
