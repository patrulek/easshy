#!/bin/bash

if [ "$1" = "true" ]; then
    shift
    set -x
fi

go test -timeout 120s -run ^TestIntegration$ -tags integration $@ github.com/patrulek/easshy/testing
