package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level represents the severity of a log message.
type Level int

const (
	LevelDebug Level = iota + 1
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[string]Level{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

// Logger writes application logs with daily rotation.
type Logger struct {
	dir         string
	level       Level
	timeNow     func() time.Time
	mu          sync.Mutex
	currentDate string
	file        *os.File
}

// NewLogger creates a logger rooted at the user's home directory.
func NewLogger(home string, level string) (*Logger, error) {
	ll := parseLevel(level)
	dir := filepath.Join(home, ".humble-ai-cli", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	return &Logger{
		dir:     dir,
		level:   ll,
		timeNow: time.Now,
	}, nil
}

// SetTimeNow replaces the time supplier; primarily for testing.
func (l *Logger) SetTimeNow(fn func() time.Time) {
	l.mu.Lock()
	l.timeNow = fn
	l.mu.Unlock()
}

// LevelEnabled reports whether the provided level should be emitted.
func (l *Logger) LevelEnabled(level Level) bool {
	return level >= l.level
}

// Debugf logs a formatted debug message.
func (l *Logger) Debugf(format string, args ...any) {
	l.logf(LevelDebug, "DEBUG", format, args...)
}

// Infof logs a formatted info message.
func (l *Logger) Infof(format string, args ...any) {
	l.logf(LevelInfo, "INFO", format, args...)
}

// Warnf logs a formatted warning message.
func (l *Logger) Warnf(format string, args ...any) {
	l.logf(LevelWarn, "WARN", format, args...)
}

// Errorf logs a formatted error message.
func (l *Logger) Errorf(format string, args ...any) {
	l.logf(LevelError, "ERROR", format, args...)
}

func (l *Logger) logf(level Level, label, format string, args ...any) {
	if l == nil || !l.LevelEnabled(level) {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.timeNow()
	date := now.Format("2006-01-02")
	if err := l.ensureFile(date); err != nil {
		return
	}

	timestamp := now.Format(time.RFC3339)
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	fmt.Fprintf(l.file, "%s [%s] %s\n", timestamp, label, message)
}

func (l *Logger) ensureFile(date string) error {
	if l.file != nil && l.currentDate == date {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}

	filename := fmt.Sprintf("application-hac-%s.log", date)
	path := filepath.Join(l.dir, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = file
	l.currentDate = date
	return nil
}

func parseLevel(value string) Level {
	if lvl, ok := levelNames[strings.ToLower(strings.TrimSpace(value))]; ok {
		return lvl
	}
	return LevelInfo
}
