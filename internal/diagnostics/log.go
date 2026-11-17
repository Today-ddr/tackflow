package diagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxBytes   = 1 << 20
	defaultMaxFiles   = 3
	defaultMaxEntries = 500
)

var (
	tokenPattern    = regexp.MustCompile(`(?i)tk_[a-z0-9_-]{8,}`)
	bearerPattern   = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/-]+=*`)
	secretPattern   = regexp.MustCompile(`(?i)(password|token|secret|cookie|authorization)(["'\s:=]+)[^\s,}&]+`)
	urlUserPattern  = regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`)
	urlQueryPattern = regexp.MustCompile(`([?&])[^#\s]+`)
	controlPattern  = regexp.MustCompile(`[\x00-\x1f\x7f]+`)
)

type Options struct {
	MaxBytes   int64
	MaxFiles   int
	MaxEntries int
}

type Entry struct {
	Time       string `json:"time"`
	Level      string `json:"level"`
	Event      string `json:"event"`
	RequestID  string `json:"request_id,omitempty"`
	Method     string `json:"method,omitempty"`
	Route      string `json:"route,omitempty"`
	Status     int    `json:"status,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Count      int64  `json:"count,omitempty"`
}

type Filter struct {
	Level     string
	RequestID string
	Limit     int
}

type Log struct {
	mu        sync.Mutex
	directory string
	file      *os.File
	fallback  io.Writer
	options   Options
	entries   []Entry
	fileSize  int64
	closed    bool
}

func Open(directory string, fallback io.Writer, options Options) (*Log, error) {
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultMaxBytes
	}
	if options.MaxFiles <= 0 {
		options.MaxFiles = defaultMaxFiles
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = defaultMaxEntries
	}
	if fallback == nil {
		fallback = io.Discard
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	log := &Log{directory: directory, fallback: fallback, options: options, entries: make([]Entry, 0, options.MaxEntries)}
	if err := log.load(); err != nil {
		return nil, err
	}
	if err := log.openCurrent(); err != nil {
		return nil, err
	}
	return log, nil
}

func (log *Log) Handler() slog.Handler {
	return &handler{log: log, attrs: make([]slog.Attr, 0)}
}

func (log *Log) Entries(filter Filter) []Entry {
	if filter.Limit <= 0 || filter.Limit > defaultMaxEntries {
		filter.Limit = defaultMaxEntries
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	entries := make([]Entry, 0, filter.Limit)
	for index := len(log.entries) - 1; index >= 0 && len(entries) < filter.Limit; index-- {
		entry := log.entries[index]
		if filter.Level != "" && entry.Level != filter.Level {
			continue
		}
		if filter.RequestID != "" && entry.RequestID != filter.RequestID {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func (log *Log) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.closed = true
	if log.file == nil {
		return nil
	}
	err := log.file.Close()
	log.file = nil
	return err
}

func (log *Log) load() error {
	files, err := filepath.Glob(filepath.Join(log.directory, "tack-*.jsonl"))
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1<<16)
		for scanner.Scan() {
			var entry Entry
			if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.Time != "" && entry.Level != "" {
				log.appendEntry(entry)
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return scanErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (log *Log) openCurrent() error {
	filename := filepath.Join(log.directory, "tack-0.jsonl")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	log.file = file
	log.fileSize = info.Size()
	return nil
}

func (log *Log) rotate(size int64) error {
	if log.file != nil {
		if err := log.file.Close(); err != nil {
			return err
		}
		log.file = nil
	}
	if err := os.Remove(filepath.Join(log.directory, "tack-"+itoa(log.options.MaxFiles-1)+".jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := log.options.MaxFiles - 2; index >= 0; index-- {
		oldName := filepath.Join(log.directory, "tack-"+itoa(index)+".jsonl")
		newName := filepath.Join(log.directory, "tack-"+itoa(index+1)+".jsonl")
		if err := os.Rename(oldName, newName); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := log.openCurrent(); err != nil {
		return err
	}
	if size > log.options.MaxBytes {
		return errors.New("diagnostic entry exceeds rotation limit")
	}
	return nil
}

func (log *Log) write(entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return os.ErrClosed
	}
	if log.fileSize+int64(len(encoded)) > log.options.MaxBytes {
		if err := log.rotate(int64(len(encoded))); err != nil {
			return err
		}
	}
	if _, err := log.file.Write(encoded); err != nil {
		return err
	}
	log.fileSize += int64(len(encoded))
	log.appendEntry(entry)
	_, _ = log.fallback.Write(encoded)
	return nil
}

func (log *Log) appendEntry(entry Entry) {
	log.entries = append(log.entries, entry)
	if overflow := len(log.entries) - log.options.MaxEntries; overflow > 0 {
		copy(log.entries, log.entries[overflow:])
		log.entries = log.entries[:log.options.MaxEntries]
	}
}

type handler struct {
	log   *Log
	attrs []slog.Attr
	group string
}

func (logger *handler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (logger *handler) Handle(_ context.Context, record slog.Record) error {
	entry := Entry{Time: record.Time.UTC().Format(time.RFC3339Nano), Level: strings.ToUpper(record.Level.String()), Event: safeEvent(record.Message)}
	attrs := append([]slog.Attr(nil), logger.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	for _, attr := range attrs {
		if attr.Equal(slog.Attr{}) || attr.Value.Kind() == slog.KindGroup {
			continue
		}
		value := attr.Value.Resolve()
		switch attr.Key {
		case "request_id":
			entry.RequestID = safeIdentifier(value.String(), 64)
		case "method":
			entry.Method = safeMethod(value.String())
		case "route":
			entry.Route = safeRoute(value.String())
		case "status":
			if value.Kind() == slog.KindInt64 {
				entry.Status = int(value.Int64())
			}
		case "duration_ms":
			if value.Kind() == slog.KindInt64 {
				entry.DurationMS = value.Int64()
			}
		case "error_code":
			entry.ErrorCode = safeIdentifier(value.String(), 80)
		case "error":
			entry.Error = redact(safeString(value.Any()))
		case "count":
			if value.Kind() == slog.KindInt64 {
				entry.Count = value.Int64()
			}
		}
	}
	return logger.log.write(entry)
}

func (logger *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{log: logger.log, attrs: append(append([]slog.Attr(nil), logger.attrs...), attrs...), group: logger.group}
}

func (logger *handler) WithGroup(name string) slog.Handler {
	return &handler{log: logger.log, attrs: logger.attrs, group: name}
}

func safeEvent(value string) string {
	switch value {
	case "request", "read maintenance setting", "daily backup failed", "daily backup written", "prune daily backups failed", "write maintenance setting", "purge trash failed", "purged trashed tasks", "background update check failed", "read idempotency key", "store idempotency key", "release rejected idempotency key":
		return value
	default:
		return "application"
	}
}

func safeMethod(value string) string {
	switch value {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD":
		return value
	default:
		return ""
	}
}

func safeRoute(value string) string {
	if len(value) > 180 || !strings.Contains(value, " /") || strings.ContainsAny(value, "?\r\n") {
		return ""
	}
	method, route, ok := strings.Cut(value, " ")
	if !ok || safeMethod(method) == "" || !strings.HasPrefix(route, "/") || strings.Contains(route, "//") {
		return ""
	}
	for _, segment := range strings.Split(route, "/") {
		if segment == "" || segment == "{id}" || segment == "{id...}" || segment == "{path...}" {
			continue
		}
		if !identifierPattern.MatchString(segment) {
			return ""
		}
	}
	return method + " " + route
}

var identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9._{}-]+$`)

func safeIdentifier(value string, maxLength int) string {
	if len(value) > maxLength || !identifierPattern.MatchString(value) {
		return ""
	}
	return value
}

func safeString(value any) string {
	message, ok := value.(error)
	if !ok {
		return ""
	}
	text := controlPattern.ReplaceAllString(message.Error(), " ")
	if len(text) > 320 {
		text = text[:320]
	}
	return text
}

func redact(value string) string {
	value = urlUserPattern.ReplaceAllString(value, "$1[redacted]@")
	value = bearerPattern.ReplaceAllString(value, "Bearer [redacted]")
	value = tokenPattern.ReplaceAllString(value, "[redacted-token]")
	value = secretPattern.ReplaceAllString(value, "$1=[redacted]")
	value = urlQueryPattern.ReplaceAllString(value, "$1[redacted-query]")
	return value
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
