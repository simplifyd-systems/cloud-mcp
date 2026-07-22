package tools

import (
	"strings"
	"testing"
)

func TestRedactLogOutput(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abc.def.ghi",
		"password=hunter2 next=value",
		"TOKEN=service-token",
		"DATABASE_URL=postgres://app:dbpass@postgres.internal/app",
		"-----BEGIN PRIVATE KEY-----\nsecret-key-material\n-----END PRIVATE KEY-----",
	}, "\n")
	output := redactLogOutput(input)
	for _, secret := range []string{"abc.def.ghi", "hunter2", "service-token", "dbpass", "secret-key-material"} {
		if strings.Contains(output, secret) {
			t.Errorf("log output contains secret %q: %s", secret, output)
		}
	}
}
