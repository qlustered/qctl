package testutil

import (
	"os"
	"testing"
)

const plaintextTokensEnv = "QCTL_ALLOW_PLAINTEXT_TOKENS"

// TestNewTestEnvRestoresTheEnvironmentWhateverTheTestDidAfterwards pins that a
// test using NewTestEnv leaves HOME and QCTL_ALLOW_PLAINTEXT_TOKENS as it found
// them, whichever way it went on to set the variable itself.
//
// The shape that used to leak is t.Setenv after NewTestEnv with the usual
// `defer env.Cleanup()`: Go runs a test's defers before its t.Cleanups, so the
// defer put the original back and t.Setenv's cleanup then restored the "1"
// NewTestEnv had forced -- into every test that ran later in the package,
// silently sending any that wanted keyring mode down the plaintext path.
func TestNewTestEnvRestoresTheEnvironmentWhateverTheTestDidAfterwards(t *testing.T) {
	shapes := map[string]func(t *testing.T){
		"t.Setenv after a deferred Cleanup": func(t *testing.T) {
			env := NewTestEnv(t)
			defer env.Cleanup()
			t.Setenv(plaintextTokensEnv, "1")
			t.Setenv("HOME", t.TempDir())
		},
		"t.Setenv with Cleanup registered by the test": func(t *testing.T) {
			env := NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			t.Setenv(plaintextTokensEnv, "")
		},
		"t.Setenv and no Cleanup at all": func(t *testing.T) {
			NewTestEnv(t)
			t.Setenv(plaintextTokensEnv, "1")
		},
		"deferred env.SetEnv": func(t *testing.T) {
			env := NewTestEnv(t)
			defer env.Cleanup()
			defer env.SetEnv(plaintextTokensEnv, "")()
		},
		"two environments in one test": func(t *testing.T) {
			outer := NewTestEnv(t)
			defer outer.Cleanup()
			inner := NewTestEnv(t)
			defer inner.Cleanup()
			t.Setenv(plaintextTokensEnv, "1")
		},
	}

	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			// Distinctive originals, so a restore to "unset" or to the harness's
			// "1" cannot pass for a restore to what was really there.
			t.Setenv(plaintextTokensEnv, "original-plaintext")
			t.Setenv("HOME", "/original-home")

			t.Run("test body", shape)

			if got := os.Getenv(plaintextTokensEnv); got != "original-plaintext" {
				t.Errorf("%s = %q after the test, want %q", plaintextTokensEnv, got, "original-plaintext")
			}
			if got := os.Getenv("HOME"); got != "/original-home" {
				t.Errorf("HOME = %q after the test, want %q", got, "/original-home")
			}
		})
	}
}

// TestSetupCredentialLeavesThePlaintextOptInAlone: a test that clears the
// opt-in to exercise keyring mode must still be in keyring mode after seeding
// a fallback credential. SetupCredentialNamed used to re-force it to "1",
// silently sending the rest of such a test down the plaintext path.
func TestSetupCredentialLeavesThePlaintextOptInAlone(t *testing.T) {
	env := NewTestEnv(t)
	t.Setenv(plaintextTokensEnv, "")

	env.SetupCredential("https://api.example.com:443", "b2c3d4e5-f6a7-8901-bcde-f23456789012", "tok")

	if got := os.Getenv(plaintextTokensEnv); got != "" {
		t.Errorf("%s = %q after SetupCredential, want it left cleared", plaintextTokensEnv, got)
	}
}
