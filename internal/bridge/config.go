package bridge

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	TackURL        string
	TackToken      string
	CodegURL       string
	CodegToken     string
	FolderID       int64
	Worker         string
	StatePath      string
	PollInterval   time.Duration
	RequestTimeout time.Duration
	ResultStatus   string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		TackURL: strings.TrimRight(getenv("TACK_URL"), "/"), TackToken: getenv("TACK_API_TOKEN"),
		CodegURL: strings.TrimRight(getenv("CODEG_URL"), "/"), CodegToken: getenv("CODEG_API_TOKEN"),
		Worker: getenv("TACK_BRIDGE_WORKER"), StatePath: getenv("TACK_BRIDGE_STATE"),
		PollInterval: 10 * time.Second, RequestTimeout: 15 * time.Second, ResultStatus: "review",
	}
	folderID, err := strconv.ParseInt(getenv("CODEG_PROJECT_ID"), 10, 32)
	if err != nil || folderID <= 0 {
		return Config{}, errors.New("CODEG_PROJECT_ID must be an existing positive Codeg project folder ID")
	}
	config.FolderID = folderID
	for _, setting := range []struct {
		name  string
		value *time.Duration
	}{
		{"TACK_BRIDGE_POLL_INTERVAL", &config.PollInterval},
		{"TACK_BRIDGE_HTTP_TIMEOUT", &config.RequestTimeout},
	} {
		if raw := getenv(setting.name); raw != "" {
			duration, parseErr := time.ParseDuration(raw)
			if parseErr != nil {
				return Config{}, fmt.Errorf("%s must be a duration", setting.name)
			}
			*setting.value = duration
		}
	}
	if status := getenv("TACK_BRIDGE_RESULT_STATUS"); status != "" {
		config.ResultStatus = status
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	absolute, err := filepath.Abs(config.StatePath)
	if err != nil {
		return Config{}, errors.New("TACK_BRIDGE_STATE cannot be resolved")
	}
	config.StatePath = absolute
	return config, nil
}

func (config Config) validate() error {
	for _, setting := range []struct{ name, value string }{
		{"TACK_URL", config.TackURL}, {"CODEG_URL", config.CodegURL},
	} {
		parsed, err := url.Parse(setting.value)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.Opaque != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%s must be an HTTP(S) base URL without credentials, query or fragment", setting.name)
		}
		if parsed.Scheme == "http" {
			address := net.ParseIP(parsed.Hostname())
			if parsed.Hostname() != "localhost" && (address == nil || !address.IsLoopback()) {
				return fmt.Errorf("%s requires HTTPS except on loopback", setting.name)
			}
		}
		if parsed.Port() != "" {
			port, parseErr := strconv.Atoi(parsed.Port())
			if parseErr != nil || port < 1 || port > 65535 {
				return fmt.Errorf("%s has an invalid port", setting.name)
			}
		}
	}
	for _, setting := range []struct{ name, value string }{
		{"TACK_API_TOKEN", config.TackToken}, {"CODEG_API_TOKEN", config.CodegToken},
	} {
		if setting.value == "" || strings.IndexFunc(setting.value, func(character rune) bool { return unicode.IsSpace(character) || unicode.IsControl(character) }) >= 0 {
			return fmt.Errorf("%s is required and must not contain whitespace", setting.name)
		}
	}
	if config.FolderID <= 0 || config.FolderID > 2147483647 {
		return errors.New("CODEG_PROJECT_ID must be a positive 32-bit folder ID")
	}
	if strings.TrimSpace(config.Worker) == "" || len(config.Worker) > 100 || strings.IndexFunc(config.Worker, unicode.IsControl) >= 0 {
		return errors.New("TACK_BRIDGE_WORKER is required (maximum 100 bytes)")
	}
	if strings.TrimSpace(config.StatePath) == "" {
		return errors.New("TACK_BRIDGE_STATE is required")
	}
	if config.PollInterval < time.Second || config.PollInterval > time.Minute {
		return errors.New("TACK_BRIDGE_POLL_INTERVAL must be between 1s and 1m")
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > time.Minute {
		return errors.New("TACK_BRIDGE_HTTP_TIMEOUT must be between 1s and 1m")
	}
	if config.ResultStatus != "review" && config.ResultStatus != "done" {
		return errors.New("TACK_BRIDGE_RESULT_STATUS must be review or done")
	}
	return nil
}
