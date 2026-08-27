package style

import (
	"strings"
	"testing"
)

func reset(t *testing.T) {
	t.Helper()
	prev := showValues
	showValues = false
	t.Cleanup(func() { showValues = prev })
}

func TestCredentialsAreMasked(t *testing.T) {
	reset(t)

	// Real shapes: an AWS secret, an access key id, a bearer token.
	for _, tc := range []struct{ key, value string }{
		{"AWS_SECRET_ACCESS_KEY", "EXAMPLEfakeSECRETvalue0123456789abcdefGH"},
		{"AWS_ACCESS_KEY_ID", "EXAMPLEFAKEKEYID1234"},
		{"PROPEL_AUTH_API_KEY", "b3f9c2a1d4e5f60718293a4b5c6d7e8f90"},
		{"STRIPE_SECRET_KEY", "not-a-real-stripe-key-9f2c4a1b"},
		{"CRYPTO_ENCRYPTION_KEY", "9f8e7d6c5b4a39281706f5e4d3c2b1a0"},
	} {
		got := Secret(tc.key, tc.value)
		if strings.Contains(got, tc.value) {
			t.Errorf("%s leaked its value", tc.key)
		}
		if !strings.HasPrefix(got, "••••") {
			t.Errorf("%s = %q, want a masked form", tc.key, got)
		}
	}
}

func TestBenignValuesStayReadable(t *testing.T) {
	reset(t)

	// Masking these would make a diff useless for the 90% of variables that
	// are not secret.
	for _, tc := range []struct{ key, value string }{
		{"PORT", "4000"},
		{"NODE_ENV", "development"},
		{"ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:3001"},
		{"REDIS_HOST", "localhost"},
		{"AWS_DEFAULT_REGION", "ap-south-1"},
		{"LOG_LEVEL", "debug"},
		{"MONGO_URI", "mongodb://localhost:27017/acme"},
	} {
		if got := Secret(tc.key, tc.value); got != tc.value {
			t.Errorf("%s = %q, want it left alone", tc.key, got)
		}
	}
}

func TestMaskIsStableSoDiffsStayMeaningful(t *testing.T) {
	reset(t)

	// "Which of these is production?" is only answerable if the same value
	// renders the same way every time.
	a := Secret("API_SECRET", "kY3nQ8wR2tV5xZ7bN4mL6pJ9hG1fD0sA")
	b := Secret("API_SECRET", "kY3nQ8wR2tV5xZ7bN4mL6pJ9hG1fD0sA")
	c := Secret("API_SECRET", "QqW2eR4tY6uI8oP0aS1dF3gH5jK7lZ9x")

	if a != b {
		t.Error("the same value masked two different ways")
	}
	if a == c {
		t.Error("two different values masked identically — the diff says nothing")
	}
}

func TestUnnamedCredentialsAreStillCaught(t *testing.T) {
	reset(t)

	// The whole reason not to rely on the key name alone.
	got := Secret("PAC_S", "Xk92Lm4Pq7Rt5Vw8Zb3Nc6Hd1Jf0Sg")
	if !strings.HasPrefix(got, "••••") {
		t.Errorf("got %q, want a high-entropy value masked whatever it is called", got)
	}
}

func TestShowValuesReveals(t *testing.T) {
	reset(t)
	showValues = true

	const secret = "EXAMPLEfakeSECRETvalue0123456789abcdefGH"
	if got := Secret("AWS_SECRET_ACCESS_KEY", secret); got != secret {
		t.Errorf("got %q, want the value when --show-values is set", got)
	}
}

func TestEmptyValueIsNotMasked(t *testing.T) {
	reset(t)
	if got := Secret("API_KEY", ""); got != "" {
		t.Errorf("got %q, want empty — masking nothing invents a secret", got)
	}
}

func TestConnectionStringPasswordsAreRedacted(t *testing.T) {
	reset(t)

	got := Secret("MONGO_URI",
		"mongodb://svcuser:fakeDBpassw0rd99@localhost:1040/appdb?tls=true")

	if strings.Contains(got, "fakeDBpassw0rd99") {
		t.Fatalf("password leaked: %q", got)
	}
	// The host and database are the useful half of a diff — keep them.
	for _, want := range []string{"mongodb://", "svcuser", "localhost:1040", "appdb"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to still show %q", got, want)
		}
	}
}

func TestConnectionStringsWithoutPasswordsAreUntouched(t *testing.T) {
	reset(t)

	for _, uri := range []string{
		"mongodb://localhost:27017/acme",
		"redis://localhost:6379",
		"https://example.com/api/stripe/webhook-handler",
		"postgres://localhost/app",
	} {
		if got := Secret("SOME_URL", uri); got != uri {
			t.Errorf("Secret(%q) = %q, want it unchanged", uri, got)
		}
	}
}

func TestOtherCredentialBearingSchemes(t *testing.T) {
	reset(t)

	for _, uri := range []string{
		"postgres://admin:hunter2@db.internal:5432/app",
		"amqp://guest:guestpw@rabbit:5672/",
		"https://user:tok3nValue@api.example.com/hook",
	} {
		if got := Secret("URL", uri); strings.Contains(got, "hunter2") ||
			strings.Contains(got, "guestpw") || strings.Contains(got, "tok3nValue") {
			t.Errorf("Secret(%q) = %q, leaked the password", uri, got)
		}
	}
}
