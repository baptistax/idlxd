package main

import (
	"bytes"
	"context"
	"github.com/baptistax/idl/internal/config"
	"github.com/baptistax/idl/internal/fault"
	"testing"
)

func TestExitCodes(t *testing.T) {
	t.Setenv("IDL_PROVIDER", "")
	for _, tt := range []struct {
		args []string
		err  error
		code int
	}{
		{nil, nil, 2}, {[]string{"../unsafe"}, nil, 2}, {[]string{"@OWNER"}, nil, 0},
		{[]string{"owner"}, fault.New(fault.Schema, "posts", "invalid page"), 1},
		{[]string{"owner"}, fault.New(fault.Authentication, "profile", "missing session"), 1},
		{[]string{"owner"}, fault.New(fault.Partial, "posts", "incomplete"), 1},
		{[]string{"owner"}, fault.New(fault.Unsupported, "highlights", "not implemented"), 1},
	} {
		var stderr bytes.Buffer
		calls := 0
		code := run(context.Background(), tt.args, &stderr, func(_ context.Context, cfg config.Config) error {
			calls++
			if cfg.Username != "owner" {
				t.Fatal("username not normalized")
			}
			return tt.err
		})
		if code != tt.code || (code == 2 && calls != 0) || (code != 2 && calls != 1) {
			t.Fatalf("got code=%d calls=%d, expected %d", code, calls, tt.code)
		}
	}
}
