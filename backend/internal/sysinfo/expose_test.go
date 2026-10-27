package sysinfo

import "testing"

func TestExternallyExposedLoopback(t *testing.T) {
	loopback := []string{"127.0.0.1", "127.0.0.53", "127.1.2.3", "::1", "[::1]"}
	for _, a := range loopback {
		if externallyExposed(a) {
			t.Errorf("%q 是回环地址，不应视为对外", a)
		}
	}
	exposed := []string{"0.0.0.0", "::", "[::]", "192.168.1.5", "2001:db8::1"}
	for _, a := range exposed {
		if !externallyExposed(a) {
			t.Errorf("%q 应视为对外监听", a)
		}
	}
}
