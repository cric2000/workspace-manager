package app

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestInfoReportsRuntimeAndBoundsSessionMetadata(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"wayland", "wayland"}, {"x11", "x11"}, {"", "unknown"},
		{"tty", "unknown"}, {"unexpected-secret-value", "unknown"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Setenv("XDG_SESSION_TYPE", tc.input)
			t.Setenv("WORKSPACE_TEST_SECRET", "do-not-expose-this")
			info := (&InfoService{}).GetInfo()
			if info.SessionType != tc.want {
				t.Fatalf("session = %q, want %q", info.SessionType, tc.want)
			}
			if info.OS != runtime.GOOS || info.Architecture != runtime.GOARCH || info.GoVersion != runtime.Version() || info.Version != Version {
				t.Fatalf("incorrect runtime diagnostics: %+v", info)
			}
			data, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "do-not-expose-this") || strings.Contains(string(data), "unexpected-secret-value") {
				t.Fatal("diagnostics exposed unexpected environment data")
			}
		})
	}
}
