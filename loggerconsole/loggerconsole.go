// Package loggerconsole prints cordis log records to standard output.
package loggerconsole

import (
	"fmt"
	"sync"
	"time"

	"gocordis"
)

// Config controls the rendered prefix.
type Config struct {
	Colors   bool
	ShowTime bool
}

// Exporter implements cordis.Exporter.
type Exporter struct {
	config Config
	mu     sync.Mutex
	last   time.Time
}

// New registers a console exporter on ctx.
func New(ctx *cordis.Context, config Config) (*Exporter, error) {
	exporter := &Exporter{config: config}
	if _, err := ctx.AddExporter(exporter); err != nil {
		return nil, err
	}
	return exporter, nil
}

// Export prints one record.
func (e *Exporter) Export(message cordis.Message) {
	fmt.Println(e.Render(message))
}

// Render formats one record.
func (e *Exporter) Render(message cordis.Message) string {
	prefix := "[I]"
	switch message.Level {
	case cordis.LevelError:
		prefix = "[E]"
	case cordis.LevelWarn:
		prefix = "[W]"
	case cordis.LevelDebug:
		prefix = "[D]"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	output := ""
	if e.config.ShowTime {
		output += message.Time.Format("2006-01-02 15:04:05 ")
	}
	output += prefix + " " + message.Name + " " + message.String()
	e.last = message.Time
	return output
}
