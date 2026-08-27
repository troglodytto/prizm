package style

import (
	"strings"
	"testing"
)

// Every case here was printed verbatim by the first implementation. They are
// grouped by the rule that let them through.
func TestValuesThatMustNeverPrint(t *testing.T) {
	reset(t)

	cases := []struct{ why, key, value string }{
		// The old rule vetoed anything containing "/" so it would not mask
		// URLs — which exempted every base64 blob that happened to draw one.
		{"base64 key containing /", "ENCRYPTION_KEY", "hZ8kQm3vN9pL2xR7tY4wE1uI6oP0aS5d/gH8jK3l="},
		{"password containing / inside a URI", "DATABASE_URL", "postgres://app:Ab3/xYz9@db.internal:5432/app"},
		// A PEM block contains spaces and newlines.
		{"private key", "MULTILINE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"},
		// userinfo without a scheme never matched.
		{"schemeless user:pass@host", "MONGO_HOST", "admin:s3cr3tPassw0rd@cluster0.mongodb.net"},
		// One character class, so the entropy rule scored it as prose.
		{"all digits", "MERCHANT_PIN", "48273910473829104738"},
		{"all letters", "RECOVERY_PHRASE", "correcthorsebatterystapleplease"},
		// The name rule matched api_key/access_key/private_key but not "key".
		{"bare KEY suffix", "SIGNING_KEY", "hZ8kQm3vN9pL2xR7tY4wE1uI6oP0aS5d"},
		{"pwd abbreviation", "DB_PWD", "hunter2hunter2hunter2"},
		{"jwt", "SESSION_JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijklmno"},
		// A leading newline defeated the anchored regex.
		{"leading newline before a URI", "DATABASE_URL", "\npostgres://app:s3cret@db:5432/app"},
	}

	for _, c := range cases {
		got := Secret(c.key, c.value)
		if strings.Contains(got, secretBody(c.value)) {
			t.Errorf("%s: %s printed its secret\n  got: %q", c.why, c.key, got)
		}
	}
}

// secretBody is the part that must not survive: for a URI it is the password,
// otherwise the whole value.
func secretBody(v string) string {
	if m := userinfo.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return m[3]
	}
	return strings.TrimSpace(v)
}

// A credential named in the key must not have its tail printed just because
// the value happens to parse as a URI.
func TestQueryStringSecretsDoNotSurviveURIRedaction(t *testing.T) {
	reset(t)

	got := Secret("DB_PASSWORD", "https://u:p@host/callback?client_secret=abc123XYZ456")
	if strings.Contains(got, "abc123XYZ456") {
		t.Errorf("got %q, want the whole value masked when the key names a secret", got)
	}
}

// Over-masking makes a diff unreadable, so the common benign shapes still
// have to render.
func TestBenignValuesStillRender(t *testing.T) {
	reset(t)

	for _, v := range []struct{ key, value string }{
		{"PORT", "4000"},
		{"NODE_ENV", "development"},
		{"REDIS_HOST", "localhost"},
		{"AWS_DEFAULT_REGION", "ap-south-1"},
		{"ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:3001"},
		{"MONGO_URI", "mongodb://localhost:27017/app"},
		{"REACT_APP_API_URL", "https://api.example.com/api/v1"},
		{"LOG_LEVEL", "debug"},
		{"AUTHOR", "Ada Lovelace"},
		{"CERT_PATH", "/etc/ssl/certs/ca.pem"},
	} {
		if got := Secret(v.key, v.value); got != v.value {
			t.Errorf("%s = %q, want it left alone", v.key, got)
		}
	}
}
