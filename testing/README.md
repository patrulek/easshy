# Integration test

## Prerequisites

To run this test it is required to have running `docker` and installed `docker-compose` on current system. You should also ensure that docker can be run without sudo privileges.

Internet connection is also required to download docker image (unless it's already cached).

## Running

To run test just execute these commands:

```console
ssh-keygen -t rsa -b 4096 -f ./easshy.key -N "" -q
sh run.sh
```

It's also possible to pass additional flags to `go test` by passing arguments to shell script. If a first argument is equal `true` than it will execute `set -x` before starting test.

## Description

Test starts 3 containers defined in `docker-compose` file, configures them properly and starts ssh server on each of them. When testing executable discovers it can connect with a server via ssh, it starts sending commands and closes after all commands end execution.

`easshy.key` and `easshy.key.pub` are needed to properly authorize client and shouldn't be used outside this test.
