package main

import (
	"testing"
	"time"
)

func BenchmarkSleep(b *testing.B) {
	for i := 0; i < b.N; i++ {
		time.Sleep(time.Millisecond)
	}
}
