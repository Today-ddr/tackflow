package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIRequiresExplicitConfiguration(t *testing.T) {
	var output bytes.Buffer
	code := run(context.Background(), []string{"--once"}, func(string) string { return "" }, &output)
	if code == 0 || !strings.Contains(output.String(), "CODEG_PROJECT_ID") {
		t.Fatalf("unconfigured worker ran: %d %s", code, output.String())
	}
}

func TestCLIHelpAndInvalidArguments(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"--help"}, 0}, {[]string{"--once", "--check"}, 2},
		{[]string{"unexpected"}, 2}, {[]string{"--bogus"}, 2},
	} {
		var output bytes.Buffer
		if code := run(context.Background(), test.args, func(string) string { return "" }, &output); code != test.code {
			t.Fatalf("args %v: %d %s", test.args, code, output.String())
		}
	}
}
