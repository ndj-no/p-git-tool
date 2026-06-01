package infrastructure

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	levelNames = map[LogLevel]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
	}

	// Regex to mask credentials in URLs, e.g., https://username:password@github.com/... -> https://username:***@github.com/...
	credRegex = regexp.MustCompile(`(?i)(https?://)([^:]+):([^@\s/]+)@`)
)

// Logger coordinates system logs with rotation and credential masking.
type Logger struct {
	fileWriter io.Writer
	outWriter  io.Writer
	level      LogLevel
}

// Global logger instance for simple package-level functions if needed.
var globalLogger *Logger

// MaskCredentials replaces the password/token part of any HTTP/HTTPS URL with "***".
func MaskCredentials(input string) string {
	return credRegex.ReplaceAllString(input, "${1}${2}:***@")
}

// NewLogger initializes a logger writing to console and a rotating log file in the user's config directory.
func NewLogger(minLevel LogLevel) (*Logger, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config dir: %w", err)
	}

	logDir := filepath.Join(userConfigDir, "workspace-tool", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}

	logFilePath := filepath.Join(logDir, "app.log")

	// Setup lumberjack rotation: max 10MB, max 5 backup files
	rotatingFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    10, // megabytes
		MaxBackups: 5,
		MaxAge:     28,   // days
		Compress:   true, // disabled by default
	}

	logger := &Logger{
		fileWriter: rotatingFile,
		outWriter:  os.Stdout,
		level:      minLevel,
	}

	globalLogger = logger
	return logger, nil
}

// Log writes a message with the specified level, applying credential masking.
func (l *Logger) Log(level LogLevel, format string, v ...interface{}) {
	if level < l.level {
		return
	}

	levelStr := levelNames[level]
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	rawMessage := fmt.Sprintf(format, v...)
	maskedMessage := MaskCredentials(rawMessage)

	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, levelStr, maskedMessage)

	// Write to rotating file
	if l.fileWriter != nil {
		_, _ = l.fileWriter.Write([]byte(logLine))
	}

	// Write to console / stdout
	if l.outWriter != nil {
		_, _ = l.outWriter.Write([]byte(logLine))
	}
}

// Debug logs at DEBUG level.
func (l *Logger) Debug(format string, v ...interface{}) {
	l.Log(LevelDebug, format, v...)
}

// Info logs at INFO level.
func (l *Logger) Info(format string, v ...interface{}) {
	l.Log(LevelInfo, format, v...)
}

// Warn logs at WARN level.
func (l *Logger) Warn(format string, v ...interface{}) {
	l.Log(LevelWarn, format, v...)
}

// Error logs at ERROR level.
func (l *Logger) Error(format string, v ...interface{}) {
	l.Log(LevelError, format, v...)
}

// GetGlobalLogger returns the global logger instance or initializes a default console-only one if uninitialized.
func GetGlobalLogger() *Logger {
	if globalLogger == nil {
		globalLogger = &Logger{
			outWriter: os.Stdout,
			level:     LevelInfo,
		}
	}
	return globalLogger
}
