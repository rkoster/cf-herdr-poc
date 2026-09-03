package main

import "testing"

func TestAddressUsesExplicitValueOrPort(t *testing.T) {
	if got := address("127.0.0.1:9", ""); got != "127.0.0.1:9" {
		t.Fatal(got)
	}
	if got := address("", "8080"); got != ":8080" {
		t.Fatal(got)
	}
	if got := address("", ""); got != ":8080" {
		t.Fatal(got)
	}
}
