# easshy - easy ssh client

## Overview

`easshy` is a thread-safe SSH client written in Golang.

The aim of this package is to provide a simple API for executing multiple commands in a single SSH session, preserving the context from previous commands. Using this package allows you to analyze the output of each command separately, just as you would when using a shell interactively.

:warning: WARNING :warning:

This package works by parsing the default prompt for a given system's shell and has only been tested for two Linux distributions (Ubuntu and Alpine) in a limited range of use-cases. It is possible that it may not work for your use-case, so do not treat this as a production-ready package. If you encounter any problem using this package or feel that it lacks an useful feature, you can contribute by creating a new issue or submitting a pull request.

## Quick start

```go
import (
    "context"
    "time"

    "github.com/patrulek/easshy"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
    defer cancel()

    // connect to remote host
    client, err := easshy.NewClient(ctx, easshy.Config{
        URL:      "ssh://root@yourhost",
        KeyPath:  "./yourkey",
        Insecure: true,
    })
    if err != nil {
        return nil, err
    }

    defer client.Close(context.Background())

    // start new shell session
    if err := client.StartSession(context.Background()); err != nil {
        return nil, err
    }
 
    var cmds []string // your cmds to call

    for _, cmd := range cmds {
        // execute a single command
        output, err := client.Execute(context.Background(), cmd)
        if err != nil {
            // handle error
        }

        // handle command output
    }
}
```

## API

`easshy` provides two ways of executing commands on a remote host:

- Pseudo-interactive single calls
- Non-interactive batch calls

The pseudo-interactive mode was presented in the [Quick start](#quick-start) section. It works by creating a client, starting a new shell session within this connection, and executing the defined commands one by one. This mode allows you to process a command's output before executing the next command. In this mode, you have control over the client's lifetime.

The non-interactive mode works by sending a batch of commands to the remote host and waiting for the call to finish. There are three different ways to make batch calls, and during these calls, a new client is created at the beginning and closed at the end of each call:

- **Serial:** A blocking call that executes commands sequentially in a single session and returns after the last command finishes its execution.
- **Stream:** A non-blocking call that executes commands sequentially in a single session and yields a reader object that lets you read command outputs as soon as they finish their execution.
- **Parallel:** A blocking call that executes commands concurrently in separate sessions and returns after all commands finish their execution.

Commands may have an additional option that allows ignoring the error of a given command to prevent stopping the entire batch call.
It's also possible to set context to specific command or a whole batch call with `ContextCmd` struct or `WithShellContext` option appropriately.

Examples of each call can be seen in the [test file](testing/integration_test.go).

## Changelog

- **v0.1.1 - 01.05.2023**: Added ShellContext construct for a batch calls

- **v0.1.0 - 27.04.2023**: Initial version

```console
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               5            181             71            730
YAML                             3             16              1            148
Markdown                         2             36              0             80
BASH                             2              8              7             19
-------------------------------------------------------------------------------
TOTAL                           12            241             79            977
-------------------------------------------------------------------------------
```
