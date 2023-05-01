//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrulek/easshy"
	"github.com/stretchr/testify/require"
)

func TestIntegration(t *testing.T) {
	defer startServers(t)() // starts containers and defer its shutdown

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	logf(t, "Waiting for servers\n")
	up := waitServers(ctx) // wait till its possible to connect with container
	require.True(t, up)

	logf(t, "Running shell test\n")
	testShell(ctx, t)

	logf(t, "Running serial test\n")
	testSerial(ctx, t)

	logf(t, "Running stream test\n")
	testStream(ctx, t)

	logf(t, "Running parallel test\n")
	testParallel(ctx, t)
}

func startServers(t *testing.T) func() {
	logf(t, "Starting servers\n")
	cmd := exec.Command("docker-compose", "up", "-d")
	err := cmd.Run()
	require.NoError(t, err)

	return func() {
		logf(t, "Shutting down servers\n")
		cmd = exec.Command("docker-compose", "down")
		err = cmd.Run()
		require.NoError(t, err)
	}
}

func waitServers(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
			cmd := exec.Command("curl", "--http0.9", "localhost:20221")
			if err := cmd.Run(); err != nil {
				break
			}
			return true
		}
	}
}

func startShell(ctx context.Context) (*easshy.Client, error) {
	client, err := easshy.NewClient(ctx, easshy.Config{
		URL:      "root@localhost:20221",
		KeyPath:  "./easshy.key",
		Insecure: true,
	})
	if err != nil {
		return nil, err
	}

	if err := client.StartSession(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

func testShell(ctx context.Context, t *testing.T) {
	logf(t, "Starting shell\n")
	client, err := startShell(ctx)
	require.NoError(t, err)

	_, err = client.Execute(ctx, "export GO111MODULE=auto")
	require.NoError(t, err)

	_, err = client.Execute(ctx, "cd /root/benchmark")
	require.NoError(t, err)

	var wg sync.WaitGroup
	tests := 5
	wg.Add(tests)

	logf(t, "Executing commands")
	for i := 0; i < tests; i++ {
		count := 2 + i

		go func(count int) {
			defer wg.Done()

			output, err := client.Execute(ctx, fmt.Sprintf("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=%d", count))
			for err == easshy.ErrExecuting {
				time.Sleep(100 * time.Millisecond)
				output, err = client.Execute(ctx, fmt.Sprintf("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=%d", count))
			}

			logf(t, "output:\n%s\n", output)
			require.NoError(t, err)
			require.EqualValues(t, 6+count, len(strings.Split(output, "\n")))
			require.True(t, strings.HasPrefix(output, "goos:"))
		}(count)
	}

	wg.Wait()

	logf(t, "Closing connection\n")
	err = client.Close(ctx)
	require.NoError(t, err)
}

func testSerial(ctx context.Context, t *testing.T) {
	cfg := easshy.Config{
		URL:      "root@localhost:20221",
		KeyPath:  "./easshy.key",
		Insecure: true,
	}
	cmds := []easshy.ICmd{
		easshy.ContextCmd{
			Cmd:  "/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=3",
			Path: "/root/benchmark",
			Env: map[string]string{
				"GO111MODULE": "auto",
			},
		},
		easshy.OptionalCmd("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=4"),
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logf(t, "Executing commands in serial\n")
	outputs, err := easshy.Serial(ctx, cfg, cmds)

	logf(t, "outputs: %s", outputs)

	require.NoError(t, err)
	require.EqualValues(t, 2, len(outputs))
	require.EqualValues(t, 9, len(strings.Split(outputs[0], "\n")))
	require.True(t, strings.HasPrefix(outputs[0], "goos:"))
}

func testStream(ctx context.Context, t *testing.T) {
	cfg := easshy.Config{
		URL:      "root@localhost:20221",
		KeyPath:  "./easshy.key",
		Insecure: true,
	}
	cmds := []easshy.ICmd{
		easshy.OptionalCmd("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=3"),
		easshy.OptionalCmd("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=4"),
		easshy.OptionalCmd("/usr/local/go/bin/go test -benchmem -run=^$ -bench ^BenchmarkSleep$ -count=5"),
	}

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logf(t, "Streaming commands outputs\n")
	reader, err := easshy.Stream(sctx, cfg, cmds, easshy.WithShellContext(
		easshy.ShellContext{
			Path: "/root/benchmark",
			Env: map[string]string{
				"GO111MODULE": "auto",
			},
		},
	))
	require.NoError(t, err)
	count := 3

	for {
		output, err := reader.Read()
		if err == io.EOF {
			break
		}

		logf(t, "output:\n%s\n", output)
		require.EqualValues(t, 6+count, len(strings.Split(output, "\n")))
		require.True(t, strings.HasPrefix(output, "goos:"))
		count++
	}

	sctx, cancel = context.WithCancel(ctx)
	reader, err = easshy.Stream(sctx, cfg, []easshy.ICmd{easshy.Cmd("ls"), easshy.Cmd("sleep 5")})
	logf(t, "Canceling streaming before first read")
	cancel()

	_, err = reader.Read()
	require.EqualValues(t, io.ErrClosedPipe, err)
}

func testParallel(ctx context.Context, t *testing.T) {
	cfg := easshy.Config{
		URL:      "root@localhost:20221",
		KeyPath:  "./easshy.key",
		Insecure: true,
	}
	cmds := []easshy.ICmd{
		easshy.Cmd("export CGO_ENABLED=0"),
		easshy.Cmd(`echo "CGO_ENABLED=$CGO_ENABLED"`),
		easshy.Cmd("ls /root"),
		easshy.ContextCmd{
			Cmd:  "wc -l benchmark_test.go",
			Path: "/root/benchmark/",
		},
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logf(t, "Executing commands in parallel\n")
	outputs, err := easshy.Parallel(ctx, cfg, cmds)

	logf(t, "outputs: %v", outputs)

	require.NoError(t, err)
	require.EqualValues(t, 4, len(outputs))
	require.EqualValues(t, "CGO_ENABLED=\r\n", outputs[1]) // not CGO_ENABLED=0 because each command is executed in different shell environment
	require.EqualValues(t, "12 benchmark_test.go\r\n", outputs[3])
}

func logf(t *testing.T, f string, args ...any) {
	now := time.Now().Format(time.DateTime)
	f = "[%s]: " + f
	args = append([]any{now}, args...)

	t.Logf(f, args...)
}
