package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestLoopbackAddress(t *testing.T) {
	host, port, err := loopbackAddress("127.0.0.1:9191")
	if err != nil || host != "127.0.0.1" || port != 9191 {
		t.Fatalf("loopbackAddress = %q, %d, %v", host, port, err)
	}
	for _, value := range []string{"0.0.0.0:9191", "example.com:9191", "127.0.0.1", "127.0.0.1:0"} {
		if _, _, err := loopbackAddress(value); err == nil {
			t.Fatalf("loopbackAddress(%q) succeeded", value)
		}
	}
}

func TestStopAllUsesReverseStartupOrderAndJoinsErrors(t *testing.T) {
	var order []string
	wantErr := errors.New("collie stop")
	err := stopAll(context.Background(), func(context.Context) error { order = append(order, "http"); return nil }, func() { order = append(order, "reconciler") }, func(context.Context) error { order = append(order, "collie"); return wantErr })
	if !reflect.DeepEqual(order, []string{"http", "reconciler", "collie"}) {
		t.Fatalf("stop order = %#v", order)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("stopAll error = %v", err)
	}
}
