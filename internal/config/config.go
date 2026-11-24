// Package config parses trakt-mcp settings from command-line flags and
// environment variables. Each flag reads the env var of the same name,
// uppercased with dashes as underscores; a flag always wins over its env var.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// envTraktClientSecret is env-only so the secret never shows in process listings.
	envTraktClientSecret = "TRAKT_CLIENT_SECRET" //nolint:gosec // G101: env var name, not a credential
	// envEncryptionKeys is env-only so key material never shows in process listings.
	envEncryptionKeys = "ENCRYPTION_KEYS"
	// envPort is the de-facto PaaS convention (Heroku, Cloud Run, Fly) for the port to bind.
	envPort = "PORT"

	// defaultListen binds loopback only, so a bare run is never exposed by accident.
	defaultListen = "127.0.0.1:8080"
	// portListenHost binds every interface when a PaaS hands us PORT: its router must reach us.
	portListenHost = "0.0.0.0"
	// defaultDBPath keeps the SQLite file under ./data, which .gitignore excludes.
	defaultDBPath = "./data/trakt-mcp.db"
	// defaultLogLevel logs one line per tool call without personal data.
	defaultLogLevel = "info"
	// defaultMetricsPath is the Prometheus convention.
	defaultMetricsPath = "/metrics"
	// defaultRateLimitReads is per user per minute; generous for interactive agent use.
	defaultRateLimitReads = 120
	// defaultRateLimitWrites is per user per minute; lower than reads since writes cost more upstream quota.
	defaultRateLimitWrites = 30
	// defaultRateLimitGlobal is per minute across all users, so one instance stays under the upstream API's shared quota.
	defaultRateLimitGlobal = 1200
	// minMetricsPasswordLen resists online brute force on the basic-auth metrics endpoint.
	minMetricsPasswordLen = 16

	// schemeHTTPS is required for the public base URL: it is also the OAuth issuer.
	schemeHTTPS = "https"
	// schemeHTTP is accepted only for loopback hosts, for local development.
	schemeHTTP = "http"
)

// Flag names, also used to derive env var names and error messages.
const (
	flagBaseURL             = "base-url"
	flagListen              = "listen"
	flagDBPath              = "db-path"
	flagTraktClientID       = "trakt-client-id"
	flagEnableDCR           = "enable-dcr"
	flagLogLevel            = "log-level"
	flagMetrics             = "metrics"
	flagMetricsPath         = "metrics-path"
	flagMetricsListen       = "metrics-listen"
	flagMetricsUser         = "metrics-user"
	flagMetricsPassword     = "metrics-password"
	flagMetricsPasswordFile = "metrics-password-file"
	flagRateLimitReads      = "rate-limit-reads"
	flagRateLimitWrites     = "rate-limit-writes"
	flagRateLimitGlobal     = "rate-limit-global"
	flagSiteURL             = "site-url"
	flagOpenAIAppsChallenge = "openai-apps-challenge"
)

// loopbackHosts may use plain http in --base-url, for local development only.
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true}

// logLevels lists the only accepted --log-level values; slog.Level.UnmarshalText
// would also accept offsets such as "info+2", which the spec does not allow.
var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// Config is the validated server configuration.
type Config struct {
	BaseURL           *url.URL
	Listen            string
	DBPath            string
	TraktClientID     string
	TraktClientSecret string
	EncryptionKeys    string
	EnableDCR         bool
	LogLevel          slog.Level
	Metrics           bool
	MetricsPath       string
	// MetricsListen is empty when metrics share the main listener.
	MetricsListen   string
	MetricsUser     string
	MetricsPassword string
	RateLimitReads  int
	RateLimitWrites int
	// RateLimitGlobal caps requests per minute across all users.
	RateLimitGlobal int
	// SiteURL is where GET / redirects; required by serve, nil for the offline commands.
	SiteURL *url.URL
	// OpenAIAppsChallenge is the domain-verification token served at
	// /.well-known/openai-apps-challenge; empty (nullable) serves nothing.
	OpenAIAppsChallenge string
	// Args holds the positional arguments left after the flags.
	Args []string
}

// EnvName returns the env var read for a flag: the flag name in upper snake case, no prefix.
func EnvName(flagName string) string {
	return strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// raw holds flag values before validation.
type raw struct {
	baseURL, listen, dbPath, traktClientID, logLevel string
	metricsPath, metricsListen, metricsUser          string
	metricsPassword, metricsPasswordFile             string
	siteURL, openAIAppsChallenge                     string
	enableDCR, metrics                               bool
	rateLimitReads, rateLimitWrites, rateLimitGlobal int
}

// newFlagSet registers every flag with its default taken from the env var when
// set, so flag.Parse lets an explicit flag override the env value.
func newFlagSet(getenv func(string) string, r *raw) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet("trakt-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	str := func(p *string, name, def, usage string) {
		if v := getenv(EnvName(name)); v != "" {
			def = v
		}

		fs.StringVar(p, name, def, usage)
	}

	var errs []error

	boolean := func(p *bool, name string, def bool, usage string) {
		if v := getenv(EnvName(name)); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid boolean %q", EnvName(name), v))
			} else {
				def = b
			}
		}

		fs.BoolVar(p, name, def, usage)
	}
	integer := func(p *int, name string, def int, usage string) {
		if v := getenv(EnvName(name)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: invalid integer %q", EnvName(name), v))
			} else {
				def = n
			}
		}

		fs.IntVar(p, name, def, usage)
	}

	listenDefault := defaultListen
	if port := getenv(envPort); port != "" {
		listenDefault = net.JoinHostPort(portListenHost, port)
	}

	str(&r.baseURL, flagBaseURL, "", "public HTTPS URL, also the OAuth issuer (required)")
	str(&r.listen, flagListen, listenDefault, "listen address")
	str(&r.dbPath, flagDBPath, defaultDBPath, "SQLite file")
	str(&r.traktClientID, flagTraktClientID, "", "Trakt application client id (required)")
	boolean(&r.enableDCR, flagEnableDCR, true, "enable OAuth Dynamic Client Registration")
	str(&r.logLevel, flagLogLevel, defaultLogLevel, "debug, info, warn or error")
	boolean(&r.metrics, flagMetrics, false, "enable the Prometheus endpoint")
	str(&r.metricsPath, flagMetricsPath, defaultMetricsPath, "metrics endpoint path")
	str(&r.metricsListen, flagMetricsListen, "", "separate metrics listen address (default: main listener)")
	str(&r.metricsUser, flagMetricsUser, "", "metrics basic-auth user (required with --metrics)")
	str(&r.metricsPassword, flagMetricsPassword, "", "metrics basic-auth password, 16+ characters")
	str(&r.metricsPasswordFile, flagMetricsPasswordFile, "", "file holding the metrics basic-auth password")
	integer(&r.rateLimitReads, flagRateLimitReads, defaultRateLimitReads, "per-user reads per minute")
	integer(&r.rateLimitWrites, flagRateLimitWrites, defaultRateLimitWrites, "per-user writes per minute")
	integer(&r.rateLimitGlobal, flagRateLimitGlobal, defaultRateLimitGlobal, "requests per minute across all users")
	str(&r.siteURL, flagSiteURL, "", "website URL that GET / redirects to")
	str(&r.openAIAppsChallenge, flagOpenAIAppsChallenge, "", "OpenAI domain-verification token served at /.well-known/openai-apps-challenge (empty: 404)")

	return fs, errors.Join(errs...)
}

// Usage writes the flag list and env-only secrets to w.
func Usage(w io.Writer) {
	fs, err := newFlagSet(func(string) string { return "" }, &raw{})
	if err != nil {
		_, _ = fmt.Fprintln(w, err)
		return
	}

	fs.SetOutput(w)
	fs.PrintDefaults()

	_, _ = fmt.Fprintln(w, "\nEvery flag also reads the env var of the same name, uppercased with dashes as underscores (--db-path: DB_PATH); the flag wins.")
	_, _ = fmt.Fprintf(w, "Env only (required): %s, %s\n", envTraktClientSecret, envEncryptionKeys)
}

// Subcommands that read configuration. genkey and version need none.
const (
	CmdServe      = "serve"
	CmdRekey      = "rekey"
	CmdDisconnect = "disconnect"
)

// Keys of the values a command may require; flags carry a "--" prefix in errors.
const (
	reqBaseURL           = "--" + flagBaseURL
	reqSiteURL           = "--" + flagSiteURL
	reqDBPath            = "--" + flagDBPath
	reqTraktClientID     = "--" + flagTraktClientID
	reqTraktClientSecret = envTraktClientSecret
	reqEncryptionKeys    = envEncryptionKeys
)

// commandSpec lists what each command needs, so offline maintenance commands
// do not demand server-only settings such as --base-url.
type commandSpec struct {
	required []string
	// nargs is the exact number of positional arguments accepted.
	nargs int
	// argsUsage names the positional arguments in error messages.
	argsUsage string
}

var commandSpecs = map[string]commandSpec{
	CmdServe: {required: []string{
		reqBaseURL, reqSiteURL, reqDBPath, reqTraktClientID, reqTraktClientSecret,
		reqEncryptionKeys,
	}},
	CmdRekey: {required: []string{reqDBPath, reqEncryptionKeys}},
	CmdDisconnect: {
		required:  []string{reqDBPath, reqEncryptionKeys, reqTraktClientID, reqTraktClientSecret},
		nargs:     1,
		argsUsage: "<trakt-slug>",
	},
}

// validateOpenAIAppsChallenge rejects a token with inner whitespace. OpenAI
// compares the served body to the token byte for byte, so such a token could
// never match and is a configuration mistake.
func validateOpenAIAppsChallenge(cfg Config) []error {
	if strings.ContainsAny(cfg.OpenAIAppsChallenge, " \t\r\n") {
		return []error{fmt.Errorf("--%s must not contain whitespace", flagOpenAIAppsChallenge)}
	}

	return nil
}

// Load parses args (without the subcommand) and env into a validated Config
// for cmd. Flags must come before positional arguments, which land in
// Config.Args. It returns flag.ErrHelp when -h or --help is given.
func Load(cmd string, args []string, getenv func(string) string) (Config, error) {
	spec, ok := commandSpecs[cmd]
	if !ok {
		return Config{}, fmt.Errorf("unknown command %q", cmd)
	}

	var r raw

	fs, err := newFlagSet(getenv, &r)
	if err != nil {
		return Config{}, err
	}

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Listen:            r.listen,
		DBPath:            r.dbPath,
		TraktClientID:     strings.TrimSpace(r.traktClientID),
		TraktClientSecret: getenv(envTraktClientSecret),
		EncryptionKeys:    getenv(envEncryptionKeys),
		EnableDCR:         r.enableDCR,
		Metrics:           r.metrics,
		MetricsPath:       r.metricsPath,
		MetricsListen:     r.metricsListen,
		MetricsUser:       r.metricsUser,
		RateLimitReads:    r.rateLimitReads,
		RateLimitWrites:   r.rateLimitWrites,
		RateLimitGlobal:   r.rateLimitGlobal,
		// Surrounding whitespace, such as a newline from a secret file, is cut.
		OpenAIAppsChallenge: strings.TrimSpace(r.openAIAppsChallenge),
		Args:                fs.Args(),
	}

	errs := validateArgs(cmd, spec, cfg.Args)
	errs = append(errs, validateRequired(spec, &r, cfg)...)

	if r.baseURL != "" {
		u, err := parseHTTPSURL(flagBaseURL, r.baseURL)
		if err != nil {
			errs = append(errs, err)
		}

		cfg.BaseURL = u
	}

	if siteURL := strings.TrimSpace(r.siteURL); siteURL != "" {
		u, err := parseHTTPSURL(flagSiteURL, siteURL)
		if err != nil {
			errs = append(errs, err)
		}

		cfg.SiteURL = u
	}

	errs = append(errs, validateOpenAIAppsChallenge(cfg)...)

	level, ok := logLevels[strings.ToLower(r.logLevel)]
	if !ok {
		errs = append(errs, fmt.Errorf("--%s: invalid value %q (want debug, info, warn or error)", flagLogLevel, r.logLevel))
	}

	cfg.LogLevel = level

	errs = append(errs, validateRates(cfg)...)

	if !strings.HasPrefix(cfg.MetricsPath, "/") {
		errs = append(errs, fmt.Errorf("--%s must start with /, got %q", flagMetricsPath, cfg.MetricsPath))
	}

	password, err := metricsPassword(r.metricsPassword, r.metricsPasswordFile)
	if err != nil {
		errs = append(errs, err)
	}

	cfg.MetricsPassword = password
	if cfg.Metrics {
		errs = append(errs, validateMetricsAuth(cfg.MetricsUser, cfg.MetricsPassword)...)
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateArgs checks the positional argument count against the command spec.
func validateArgs(cmd string, spec commandSpec, args []string) []error {
	switch {
	case len(args) == spec.nargs:
		return nil
	case spec.nargs == 0:
		return []error{fmt.Errorf("%s: unexpected argument %q (flags must come first)", cmd, args[0])}
	default:
		return []error{fmt.Errorf("%s: want exactly %d argument %s, got %d", cmd, spec.nargs, spec.argsUsage, len(args))}
	}
}

// validateRequired reports every value the command requires but finds empty.
func validateRequired(spec commandSpec, r *raw, cfg Config) []error {
	values := map[string]string{
		reqBaseURL:           r.baseURL,
		reqSiteURL:           strings.TrimSpace(r.siteURL),
		reqDBPath:            cfg.DBPath,
		reqTraktClientID:     cfg.TraktClientID,
		reqTraktClientSecret: cfg.TraktClientSecret,
		reqEncryptionKeys:    cfg.EncryptionKeys,
	}

	var errs []error

	for _, name := range spec.required {
		if values[name] == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}

	return errs
}

// validateRates requires every rate limit to be positive.
func validateRates(cfg Config) []error {
	var errs []error

	if cfg.RateLimitReads <= 0 {
		errs = append(errs, fmt.Errorf("--%s must be positive, got %d", flagRateLimitReads, cfg.RateLimitReads))
	}

	if cfg.RateLimitWrites <= 0 {
		errs = append(errs, fmt.Errorf("--%s must be positive, got %d", flagRateLimitWrites, cfg.RateLimitWrites))
	}

	if cfg.RateLimitGlobal <= 0 {
		errs = append(errs, fmt.Errorf("--%s must be positive, got %d", flagRateLimitGlobal, cfg.RateLimitGlobal))
	}

	return errs
}

// parseHTTPSURL requires an absolute https URL, or http on a loopback host.
// flagName names the offending flag in errors.
func parseHTTPSURL(flagName, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", flagName, err)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("--%s: %q has no host", flagName, raw)
	}

	switch u.Scheme {
	case schemeHTTPS:
	case schemeHTTP:
		if !loopbackHosts[u.Hostname()] {
			return nil, fmt.Errorf("--%s: %q must use https (http allowed only for localhost and 127.0.0.1)", flagName, raw)
		}
	default:
		return nil, fmt.Errorf("--%s: %q must use https", flagName, raw)
	}

	return u, nil
}

// metricsPassword resolves the password from the flag or the file, never both.
func metricsPassword(value, path string) (string, error) {
	if value != "" && path != "" {
		return "", fmt.Errorf("--%s and --%s are mutually exclusive", flagMetricsPassword, flagMetricsPasswordFile)
	}

	if path == "" {
		return value, nil
	}
	// The path comes from the operator's own configuration, not from a request.
	b, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied path
	if err != nil {
		return "", fmt.Errorf("--%s: %w", flagMetricsPasswordFile, err)
	}

	return strings.TrimSpace(string(b)), nil
}

func validateMetricsAuth(user, password string) []error {
	var errs []error
	if user == "" {
		errs = append(errs, fmt.Errorf("--%s is required with --%s", flagMetricsUser, flagMetrics))
	}

	switch {
	case password == "":
		errs = append(errs, fmt.Errorf("--%s or --%s is required with --%s", flagMetricsPassword, flagMetricsPasswordFile, flagMetrics))
	case utf8.RuneCountInString(password) < minMetricsPasswordLen:
		errs = append(errs, fmt.Errorf("--%s must be at least %d characters", flagMetricsPassword, minMetricsPasswordLen))
	}

	return errs
}
