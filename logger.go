package cordis

import (
	"fmt"
	"sync"
	"time"
)

// Level is a log severity. Lower values are more severe.
type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

func (l Level) String() string {
	switch l {
	case LevelError:
		return "error"
	case LevelWarn:
		return "warn"
	case LevelInfo:
		return "info"
	case LevelDebug:
		return "debug"
	default:
		return "unknown"
	}
}

// Message is one log record handed to exporters.
type Message struct {
	Seq   int64
	Time  time.Time
	Name  string
	Type  string
	Level Level
	Args  []any
}

// Exporter receives log records.
type Exporter interface {
	Export(Message)
}

// Logger writes records under a name.
type Logger struct {
	name    string
	level   Level
	service *LoggerService
}

// LoggerService owns exporters and the in-memory ring buffer.
type LoggerService struct {
	ctx        *Context
	mu         sync.Mutex
	exporters  map[int]Exporter
	next       int
	seq        int64
	buffer     []Message
	bufferSize int
}

// Logger returns a logger for name. An empty name uses the fiber name.
func (c *Context) Logger(name string) *Logger {
	if name == "" {
		name = c.fiber.Name()
	}
	return &Logger{name: name, level: LevelInfo, service: c.logger}
}

// Error logs at the error level.
func (c *Context) Error(args ...any) { c.Logger("").Error(args...) }

// Warn logs at the warn level.
func (c *Context) Warn(args ...any) { c.Logger("").Warn(args...) }

// Info logs at the info level.
func (c *Context) Info(args ...any) { c.Logger("").Info(args...) }

// Debug logs at the debug level.
func (c *Context) Debug(args ...any) { c.Logger("").Debug(args...) }

// Error logs an error.
func (l *Logger) Error(args ...any) { l.log(LevelError, args) }

// Warn logs a warning.
func (l *Logger) Warn(args ...any) { l.log(LevelWarn, args) }

// Info logs information.
func (l *Logger) Info(args ...any) { l.log(LevelInfo, args) }

// Debug logs debug information.
func (l *Logger) Debug(args ...any) { l.log(LevelDebug, args) }

func (l *Logger) log(level Level, args []any) {
	if l == nil || l.service == nil || l.level < level {
		return
	}
	service := l.service
	service.mu.Lock()
	service.seq++
	message := Message{
		Seq:   service.seq,
		Time:  time.Now(),
		Name:  l.name,
		Type:  level.String(),
		Level: level,
		Args:  args,
	}
	service.buffer = append(service.buffer, message)
	if overflow := len(service.buffer) - service.bufferSize; overflow > 0 {
		service.buffer = service.buffer[overflow:]
	}
	exporters := make([]Exporter, 0, len(service.exporters))
	for _, exporter := range service.exporters {
		exporters = append(exporters, exporter)
	}
	service.mu.Unlock()

	for _, exporter := range exporters {
		exporter.Export(message)
	}
}

// Exporter registers an exporter as an effect of the current fiber.
func (s *LoggerService) Exporter(exporter Exporter) (Disposer, error) {
	return s.ctx.fiber.effect("ctx.logger.exporter()", func() (Disposer, error) {
		s.mu.Lock()
		s.next++
		id := s.next
		s.exporters[id] = exporter
		s.mu.Unlock()
		return func() error {
			s.mu.Lock()
			delete(s.exporters, id)
			s.mu.Unlock()
			return nil
		}, nil
	})
}

// AddExporter registers an exporter on the logger service.
func (c *Context) AddExporter(exporter Exporter) (Disposer, error) {
	return c.logger.Exporter(exporter)
}

// Buffer returns a copy of the recent records.
func (s *LoggerService) Buffer() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.buffer...)
}

func formatMessage(args []any) string {
	if len(args) == 0 {
		return ""
	}
	if err, ok := args[0].(error); ok && len(args) == 1 {
		return err.Error()
	}
	if format, ok := args[0].(string); ok && len(args) > 1 {
		return fmt.Sprintf(format, args[1:]...)
	}
	return fmt.Sprint(args...)
}

// String renders the message arguments.
func (m Message) String() string { return formatMessage(m.Args) }
