package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

var (
	serviceName string
	hostname    string
)

// LogLevel represents log severity
type LogLevel string

const (
	LevelInfo  LogLevel = "INFO"
	LevelDebug LogLevel = "DEBUG"
	LevelError LogLevel = "ERROR"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Service   string                 `json:"service"`
	Action    string                 `json:"action"`
	Message   string                 `json:"message"`
	Hostname  string                 `json:"hostname"`
	RequestID string                 `json:"request_id"`
	Error     *ErrorObject           `json:"error,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// ErrorObject represents structured error information
type ErrorObject struct {
	Msg   string `json:"msg"`
	Stack string `json:"stack"`
}

// Init initializes the logger with service name
func Init(service string) {
	serviceName = service
	hostname, _ = os.Hostname()
	if hostname == "" {
		hostname = "unknown-host"
	}
}

// LogInfo logs an INFO level message
func LogInfo(action string, msg string, reqID string, args ...interface{}) {
	log(LevelInfo, action, msg, reqID, nil, args...)
}

// LogDebug logs a DEBUG level message
func LogDebug(action string, msg string, reqID string, args ...interface{}) {
	log(LevelDebug, action, msg, reqID, nil, args...)
}

// LogError logs an ERROR level message with error details
func LogError(action string, msg string, err error, reqID string, args ...interface{}) {
	if err == nil {
		log(LevelError, action, msg, reqID, nil, args...)
		return
	}

	errObj := &ErrorObject{
		Msg:   err.Error(),
		Stack: getStackTrace(3), // Skip 3 frames to get to the actual error location
	}
	log(LevelError, action, msg, reqID, errObj, args...)
}

// log is the internal logging function
func log(level LogLevel, action, msg, reqID string, errObj *ErrorObject, args ...interface{}) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     string(level),
		Service:   serviceName,
		Action:    action,
		Message:   msg,
		Hostname:  hostname,
		RequestID: reqID,
		Error:     errObj,
	}

	// Parse args as key-value pairs for details
	if len(args) > 0 {
		details := make(map[string]interface{})
		for i := 0; i < len(args)-1; i += 2 {
			if key, ok := args[i].(string); ok {
				details[key] = args[i+1]
			}
		}
		if len(details) > 0 {
			entry.Details = details
		}
	}

	// Marshal to JSON and output to stdout
	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		// Fallback to simple output if JSON marshaling fails
		fmt.Fprintf(os.Stdout, `{"timestamp":"%s","level":"ERROR","message":"Failed to marshal log entry: %v"}`+"\n",
			time.Now().UTC().Format(time.RFC3339Nano), err)
		return
	}

	fmt.Println(string(jsonBytes))
}

// getStackTrace returns a formatted stack trace
func getStackTrace(skip int) string {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(skip, pcs[:])

	if n == 0 {
		return "no stack trace available"
	}

	var sb strings.Builder
	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()
		
		// Skip runtime and logger internal frames
		if !strings.Contains(frame.Function, "runtime.") &&
			!strings.Contains(frame.Function, "logger.") {
			sb.WriteString(fmt.Sprintf("%s:%d %s\n", 
				frame.File, frame.Line, frame.Function))
		}

		if !more {
			break
		}
	}

	trace := sb.String()
	if trace == "" {
		return "stack trace filtered"
	}

	return trace
}

// GetServiceName returns the current service name
func GetServiceName() string {
	return serviceName
}

// GetHostname returns the current hostname
func GetHostname() string {
	return hostname
}
