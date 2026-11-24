package config

import (
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requiredArgs holds the minimum flags Load accepts; tests append to it.
var requiredArgs = []string{
	"--base-url=https://trakt-mcp.example.com",
	"--site-url=https://site.example.com",
	"--trakt-client-id=client-id",
}

// testEncryptionKeys is a syntactically plausible keyring entry; Load only
// checks that the value is non-empty.
const testEncryptionKeys = "k1:AAAA"

const (
	// testFromEnv is a value that only an env var supplies, to tell it from flag values.
	testFromEnv = "from-env"
	// testEnvPort is the PaaS port variable name.
	testEnvPort = "PORT"
	// testListenFromEnv is a LISTEN env value distinct from every default.
	testListenFromEnv = "127.0.0.1:6000"
)

// requiredEnv holds the env-only secrets Load requires.
var requiredEnv = map[string]string{
	envTraktClientSecret: "client-secret",
	envEncryptionKeys:    testEncryptionKeys,
}

// envFunc builds an injected env lookup from requiredEnv plus extra, so tests
// never touch the process environment and can run in parallel.
func envFunc(extra map[string]string) func(string) string {
	env := map[string]string{}
	maps.Copy(env, requiredEnv)

	maps.Copy(env, extra)

	return func(key string) string { return env[key] }
}

func withArgs(extra ...string) []string {
	return append(append([]string{}, requiredArgs...), extra...)
}

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs(), envFunc(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("Listen = %q", cfg.Listen)
	}

	if cfg.DBPath != "./data/trakt-mcp.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}

	if !cfg.EnableDCR {
		t.Error("EnableDCR = false, want true")
	}

	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}

	if cfg.Metrics {
		t.Error("Metrics = true, want false")
	}

	if cfg.MetricsPath != "/metrics" {
		t.Errorf("MetricsPath = %q", cfg.MetricsPath)
	}

	if cfg.RateLimitReads != 120 {
		t.Errorf("RateLimitReads = %d", cfg.RateLimitReads)
	}

	if cfg.RateLimitWrites != 30 {
		t.Errorf("RateLimitWrites = %d", cfg.RateLimitWrites)
	}

	if cfg.RateLimitGlobal != 1200 {
		t.Errorf("RateLimitGlobal = %d, want 1200", cfg.RateLimitGlobal)
	}
}

// TestLoad_ProvidedValues checks the values Load copies from the required
// flags and env-only secrets.
func TestLoad_ProvidedValues(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs(), envFunc(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BaseURL == nil || cfg.BaseURL.String() != "https://trakt-mcp.example.com" {
		t.Errorf("BaseURL = %v", cfg.BaseURL)
	}

	if cfg.TraktClientSecret != "client-secret" || cfg.EncryptionKeys != testEncryptionKeys {
		t.Errorf("secrets not read from env: %q %q", cfg.TraktClientSecret, cfg.EncryptionKeys)
	}

	if cfg.SiteURL == nil || cfg.SiteURL.String() != "https://site.example.com" {
		t.Errorf("SiteURL = %v, want the required --site-url", cfg.SiteURL)
	}
}

func TestLoad_SiteURL(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"https://example.com":        true,
		"https://example.com/":       true,
		"https://example.com/about":  true,
		"http://localhost:4321":      true,
		"http://example.com":         false,
		"ftp://example.com":          false,
		"https://":                   false,
		"not a url":                  false,
		"//example.com":              false,
		"http://localhost.evil.com/": false,
	}
	for raw, ok := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(CmdServe, argsWithout("--site-url", "--site-url="+raw), envFunc(nil))
			if !ok {
				if err == nil || !strings.Contains(err.Error(), "site-url") {
					t.Errorf("err = %v, want error naming site-url", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}

			if cfg.SiteURL == nil || cfg.SiteURL.String() != raw {
				t.Errorf("SiteURL = %v, want %q", cfg.SiteURL, raw)
			}
		})
	}
}

func TestLoad_SiteURLFromEnv(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, argsWithout("--site-url"), envFunc(map[string]string{"SITE_URL": "https://env-site.example.com"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SiteURL == nil || cfg.SiteURL.String() != "https://env-site.example.com" {
		t.Errorf("SiteURL = %v", cfg.SiteURL)
	}
}

func TestLoad_OpenAIAppsChallenge(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		arg  string
		want string
		ok   bool
	}{
		"unset":                {arg: "", want: "", ok: true},
		"token":                {arg: "abc123", want: "abc123", ok: true},
		"outer whitespace cut": {arg: "  abc123\n", want: "abc123", ok: true},
		"inner space":          {arg: "abc 123", ok: false},
		"inner newline":        {arg: "abc\n123", ok: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg, err := Load(CmdServe, withArgs("--openai-apps-challenge="+tc.arg), envFunc(nil))
			if !tc.ok {
				if err == nil || !strings.Contains(err.Error(), "openai-apps-challenge") {
					t.Errorf("err = %v, want error naming openai-apps-challenge", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}

			if cfg.OpenAIAppsChallenge != tc.want {
				t.Errorf("OpenAIAppsChallenge = %q, want %q", cfg.OpenAIAppsChallenge, tc.want)
			}
		})
	}
}

func TestLoad_OpenAIAppsChallengeFromEnv(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs(), envFunc(map[string]string{"OPENAI_APPS_CHALLENGE": testFromEnv}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.OpenAIAppsChallenge != testFromEnv {
		t.Errorf("OpenAIAppsChallenge = %q", cfg.OpenAIAppsChallenge)
	}
}

// TestRemovedFlagsRejected pins the removal of flags with no Trakt equivalent:
// passing one is an unknown-flag error.
func TestRemovedFlagsRejected(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--operator-name=Jane Doe", "--operator-email=jane@example.com", "--allowed-domains=acme.com", "--google-client-id=x"} {
		if _, err := Load(CmdServe, withArgs(flag), envFunc(nil)); err == nil {
			t.Errorf("%s: err = nil, want unknown-flag error", flag)
		}
	}
}

func TestPortEnvDefaultsListen(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs(), envFunc(map[string]string{testEnvPort: "9000"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "0.0.0.0:9000" {
		t.Errorf("Listen = %q, want 0.0.0.0:9000", cfg.Listen)
	}

	cfg, err = Load(CmdServe, withArgs("--listen=127.0.0.1:7000"), envFunc(map[string]string{testEnvPort: "9000"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "127.0.0.1:7000" {
		t.Errorf("Listen = %q, want flag value", cfg.Listen)
	}

	cfg, err = Load(CmdServe, withArgs(), envFunc(map[string]string{testEnvPort: "9000", "LISTEN": testListenFromEnv}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != testListenFromEnv {
		t.Errorf("Listen = %q, want LISTEN value", cfg.Listen)
	}
}

func TestLoad_EnvAndFlagPrecedence(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs("--db-path=/b"), envFunc(map[string]string{"DB_PATH": "/a"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DBPath != "/b" {
		t.Errorf("DBPath = %q, want /b", cfg.DBPath)
	}

	cfg, err = Load(CmdServe, withArgs(), envFunc(map[string]string{
		"DB_PATH":           "/a",
		"ENABLE_DCR":        "false",
		"RATE_LIMIT_READS":  "10",
		"LOG_LEVEL":         "debug",
		"METRICS_PATH":      "/m",
		"RATE_LIMIT_WRITES": "5",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DBPath != "/a" || cfg.EnableDCR || cfg.RateLimitReads != 10 || cfg.RateLimitWrites != 5 ||
		cfg.LogLevel != slog.LevelDebug || cfg.MetricsPath != "/m" {
		t.Errorf("env not applied: %+v", cfg)
	}
}

func TestLoad_InvalidEnv(t *testing.T) {
	t.Parallel()

	if _, err := Load(CmdServe, withArgs(), envFunc(map[string]string{"RATE_LIMIT_READS": "abc"})); err == nil ||
		!strings.Contains(err.Error(), "RATE_LIMIT_READS") {
		t.Errorf("bad int env: err = %v, want error naming RATE_LIMIT_READS", err)
	}

	if _, err := Load(CmdServe, withArgs(), envFunc(map[string]string{"ENABLE_DCR": "maybe"})); err == nil ||
		!strings.Contains(err.Error(), "ENABLE_DCR") {
		t.Errorf("bad bool env: err = %v, want error naming ENABLE_DCR", err)
	}
}

func TestLoad_SecretsEnvOnly(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--trakt-client-secret=x", "--encryption-keys=x"} {
		if _, err := Load(CmdServe, withArgs(flag), envFunc(nil)); err == nil {
			t.Errorf("%s: err = nil, want unknown-flag error", flag)
		}
	}
}

func TestLoad_Required(t *testing.T) {
	t.Parallel()

	flagCases := map[string]string{
		"base-url":        "--base-url",
		"site-url":        "--site-url",
		"trakt-client-id": "--trakt-client-id",
	}
	for name, prefix := range flagCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var args []string

			for _, a := range requiredArgs {
				if !strings.HasPrefix(a, prefix+"=") {
					args = append(args, a)
				}
			}

			_, err := Load(CmdServe, args, envFunc(nil))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v, want error naming %s", err, name)
			}
		})
	}

	for _, key := range []string{"TRAKT_CLIENT_SECRET", envEncryptionKeys} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, err := Load(CmdServe, withArgs(), envFunc(map[string]string{key: ""}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("err = %v, want error naming %s", err, key)
			}
		})
	}
}

func TestLoad_BaseURLMustBeHTTPS(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"http://x":                  false,
		"http://example.com":        false,
		"ftp://example.com":         false,
		"https://":                  false,
		"not a url":                 false,
		"https://example.com":       true,
		"https://example.com/mcp/":  true,
		"http://localhost":          true,
		"http://localhost:8080":     true,
		"http://127.0.0.1":          true,
		"http://127.0.0.1:8080":     true,
		"http://localhost.evil.com": false,
	}
	for raw, ok := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			args := []string{
				"--base-url=" + raw,
				"--site-url=https://site.example.com",
				"--trakt-client-id=client-id",
			}

			_, err := Load(CmdServe, args, envFunc(nil))
			if ok && err != nil {
				t.Errorf("err = %v, want nil", err)
			}

			if !ok && err == nil {
				t.Error("err = nil, want error")
			}
		})
	}
}

func TestLoad_Metrics(t *testing.T) {
	t.Parallel()

	const (
		pw15 = "123456789012345"
		pw16 = "1234567890123456"
	)

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-password="+pw16), envFunc(nil)); err == nil ||
		!strings.Contains(err.Error(), "metrics-user") {
		t.Errorf("missing user: err = %v", err)
	}

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u"), envFunc(nil)); err == nil ||
		!strings.Contains(err.Error(), "metrics-password") {
		t.Errorf("missing password: err = %v", err)
	}

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password="+pw15), envFunc(nil)); err == nil {
		t.Error("15-char password: err = nil, want error")
	}

	cfg, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password="+pw16), envFunc(nil))
	if err != nil {
		t.Fatalf("16-char password: %v", err)
	}

	if !cfg.Metrics || cfg.MetricsUser != "u" || cfg.MetricsPassword != pw16 {
		t.Errorf("cfg = %+v", cfg)
	}

	cfg, err = Load(CmdServe, withArgs("--metrics", "--metrics-user=u"), envFunc(map[string]string{"METRICS_PASSWORD": pw16}))
	if err != nil {
		t.Fatalf("env password: %v", err)
	}

	if cfg.MetricsPassword != pw16 {
		t.Errorf("MetricsPassword = %q", cfg.MetricsPassword)
	}
}

func TestLoad_MetricsPasswordFile(t *testing.T) {
	t.Parallel()

	const (
		pw15 = "123456789012345"
		pw16 = "1234567890123456"
	)

	path := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(path, []byte("  "+pw16+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password-file="+path), envFunc(nil))
	if err != nil {
		t.Fatalf("password file: %v", err)
	}

	if cfg.MetricsPassword != pw16 {
		t.Errorf("MetricsPassword = %q, want trimmed file content", cfg.MetricsPassword)
	}

	shortPath := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(shortPath, []byte(pw15+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password-file="+shortPath), envFunc(nil)); err == nil {
		t.Error("15-char password file: err = nil, want error")
	}

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password-file="+filepath.Join(t.TempDir(), "missing")), envFunc(nil)); err == nil {
		t.Error("missing password file: err = nil, want error")
	}

	if _, err := Load(CmdServe, withArgs("--metrics", "--metrics-user=u", "--metrics-password="+pw16, "--metrics-password-file="+path), envFunc(nil)); err == nil {
		t.Error("password and password file: err = nil, want error")
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"log level":          withArgs("--log-level=verbose"),
		"zero reads":         withArgs("--rate-limit-reads=0"),
		"negative writes":    withArgs("--rate-limit-writes=-1"),
		"zero global":        withArgs("--rate-limit-global=0"),
		"metrics path slash": withArgs("--metrics-path=metrics"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Load(CmdServe, args, envFunc(nil)); err == nil {
				t.Error("err = nil, want error")
			}
		})
	}
}

func TestLoad_ServeRejectsPositional(t *testing.T) {
	t.Parallel()

	_, err := Load(CmdServe, withArgs("stray", "--enable-dcr=false"), envFunc(nil))
	if err == nil || !strings.Contains(err.Error(), `"stray"`) {
		t.Errorf("err = %v, want error naming the stray argument", err)
	}
}

func TestLoad_RekeyMinimalRequirements(t *testing.T) {
	t.Parallel()

	env := func(key string) string {
		if key == envEncryptionKeys {
			return testEncryptionKeys
		}

		return ""
	}

	cfg, err := Load(CmdRekey, nil, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DBPath != "./data/trakt-mcp.db" || cfg.EncryptionKeys != testEncryptionKeys {
		t.Errorf("cfg = %+v", cfg)
	}

	if _, err := Load(CmdRekey, nil, func(string) string { return "" }); err == nil ||
		!strings.Contains(err.Error(), envEncryptionKeys) {
		t.Errorf("missing keys: err = %v", err)
	}

	if _, err := Load(CmdRekey, []string{"--db-path="}, env); err == nil || !strings.Contains(err.Error(), "db-path") {
		t.Errorf("empty db path: err = %v", err)
	}

	if _, err := Load(CmdRekey, []string{"extra"}, env); err == nil || !strings.Contains(err.Error(), `"extra"`) {
		t.Errorf("positional: err = %v", err)
	}
}

func TestLoad_DisconnectRequiresOneArg(t *testing.T) {
	t.Parallel()

	env := func(key string) string {
		return map[string]string{
			envEncryptionKeys:     testEncryptionKeys,
			"TRAKT_CLIENT_SECRET": "secret",
		}[key]
	}
	flags := []string{"--db-path=/tmp/x.db", "--trakt-client-id=cid"}

	if _, err := Load(CmdDisconnect, flags, env); err == nil {
		t.Error("0 args: err = nil, want error")
	}

	cfg, err := Load(CmdDisconnect, append(append([]string{}, flags...), "a@b.c"), env)
	if err != nil {
		t.Fatalf("1 arg: %v", err)
	}

	if len(cfg.Args) != 1 || cfg.Args[0] != "a@b.c" || cfg.DBPath != "/tmp/x.db" {
		t.Errorf("cfg = %+v", cfg)
	}

	if _, err := Load(CmdDisconnect, append(append([]string{}, flags...), "a@b.c", "d@e.f"), env); err == nil {
		t.Error("2 args: err = nil, want error")
	}

	if _, err := Load(CmdDisconnect, []string{"a@b.c"}, env); err == nil ||
		!strings.Contains(err.Error(), "trakt-client-id") {
		t.Errorf("missing client id: err = %v", err)
	}
}

func TestLoad_UnknownCommand(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"", "genkey", "version", "bogus"} {
		if _, err := Load(cmd, withArgs(), envFunc(nil)); err == nil {
			t.Errorf("%q: err = nil, want error", cmd)
		}
	}
}

func TestEnvName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"db-path":          "DB_PATH",
		"base-url":         "BASE_URL",
		"trakt-client-id":  "TRAKT_CLIENT_ID",
		"rate-limit-reads": "RATE_LIMIT_READS",
		"listen":           "LISTEN",
		"":                 "",
	}
	for flagName, want := range cases {
		if got := EnvName(flagName); got != want {
			t.Errorf("EnvName(%q) = %q, want %q", flagName, got, want)
		}
	}
}

func TestFlagBeatsEnv(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs("--trakt-client-id=from-flag"), envFunc(map[string]string{"TRAKT_CLIENT_ID": testFromEnv}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TraktClientID != "from-flag" {
		t.Errorf("TraktClientID = %q, want from-flag", cfg.TraktClientID)
	}

	cfg, err = Load(CmdServe, []string{"--base-url=https://trakt.example.com", "--site-url=https://site.example.com"}, envFunc(map[string]string{"TRAKT_CLIENT_ID": testFromEnv}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TraktClientID != testFromEnv {
		t.Errorf("TraktClientID = %q, want from-env", cfg.TraktClientID)
	}
}

// TestTraktSecretOnlyFromEnv checks the secret is read from TRAKT_CLIENT_SECRET
// and cannot be passed as a flag, so it never shows in process listings.
func TestTraktSecretOnlyFromEnv(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs(), envFunc(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TraktClientSecret != "client-secret" {
		t.Errorf("TraktClientSecret = %q", cfg.TraktClientSecret)
	}

	if _, err := Load(CmdServe, withArgs("--trakt-client-secret=x"), envFunc(nil)); err == nil {
		t.Error("--trakt-client-secret: err = nil, want unknown-flag error")
	}

	if _, err := Load(CmdServe, withArgs(), envFunc(map[string]string{"TRAKT_CLIENT_SECRET": ""})); err == nil ||
		!strings.Contains(err.Error(), "TRAKT_CLIENT_SECRET") {
		t.Errorf("missing secret: err = %v, want error naming TRAKT_CLIENT_SECRET", err)
	}
}

func TestLoad_RateLimitGlobal(t *testing.T) {
	t.Parallel()

	cfg, err := Load(CmdServe, withArgs("--rate-limit-global=5"), envFunc(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.RateLimitGlobal != 5 {
		t.Errorf("RateLimitGlobal = %d, want 5", cfg.RateLimitGlobal)
	}

	cfg, err = Load(CmdServe, withArgs(), envFunc(map[string]string{"RATE_LIMIT_GLOBAL": "7"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.RateLimitGlobal != 7 {
		t.Errorf("RateLimitGlobal = %d, want 7", cfg.RateLimitGlobal)
	}
}

// argsWithout returns requiredArgs minus the flag named drop (for example
// "--site-url"), plus extra.
func argsWithout(drop string, extra ...string) []string {
	var args []string

	for _, a := range requiredArgs {
		if !strings.HasPrefix(a, drop+"=") {
			args = append(args, a)
		}
	}

	return append(args, extra...)
}
