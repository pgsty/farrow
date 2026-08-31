package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"go.yaml.in/yaml/v3"
	"golang.org/x/term"
)

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
	outputYAML outputFormat = "yaml"
)

type outputContext struct {
	format      outputFormat
	verbose     bool
	stdoutTTY   bool
	stderrTTY   bool
	stderrFile  bool
	color       bool
	mu          sync.Mutex
	stdoutBytes int64
	lastError   string
}

type commandFailure struct {
	Error       string `json:"error"`
	Message     string `json:"message"`
	OperationID string `json:"operation_id,omitempty"`
}

func emitCommandFailure(stdout, stderr io.Writer, category, message, operationID string) int {
	if !structuredOutput(stdout) {
		return exitOK
	}
	return encodeJSON(stdout, stderr, commandFailure{Error: category, Message: message, OperationID: operationID})
}

func reportCommandFailure(stdout, stderr io.Writer, category, message, operationID string, code int) int {
	if encodeCode := emitCommandFailure(stdout, stderr, category, message, operationID); encodeCode != exitOK {
		return encodeCode
	}
	errorf(stderr, "%s", message)
	return code
}

type outputWriter struct {
	io.Writer
	context *outputContext
	stderr  bool
}

func (writer *outputWriter) Write(data []byte) (int, error) {
	written, err := writer.Writer.Write(data)
	if writer.context != nil && !writer.stderr && written > 0 {
		writer.context.mu.Lock()
		writer.context.stdoutBytes += int64(written)
		writer.context.mu.Unlock()
	}
	return written, err
}

func structuredPayloadWritten(writer io.Writer) bool {
	state := outputContextFrom(writer)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.stdoutBytes > 0
}

func recordCommandError(writer io.Writer, message string) {
	state := outputContextFrom(writer)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.lastError = message
	state.mu.Unlock()
}

func recordedCommandError(writer io.Writer) string {
	state := outputContextFrom(writer)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.lastError
}

func writerFile(writer io.Writer) (*os.File, bool) {
	for {
		switch value := writer.(type) {
		case *os.File:
			return value, true
		case *outputWriter:
			writer = value.Writer
		default:
			return nil, false
		}
	}
}

func writerTTY(writer io.Writer) bool {
	file, ok := writerFile(writer)
	return ok && term.IsTerminal(int(file.Fd()))
}

func outputContextFrom(writer io.Writer) *outputContext {
	if contextual, ok := writer.(*outputWriter); ok {
		return contextual.context
	}
	return nil
}

// rawWriter returns the original writer behind the presentation wrapper.
// Interactive child processes must receive the underlying *os.File so
// os/exec preserves terminal semantics instead of copying through a pipe.
func rawWriter(writer io.Writer) io.Writer {
	for {
		contextual, ok := writer.(*outputWriter)
		if !ok {
			return writer
		}
		writer = contextual.Writer
	}
}

func outputFormatFor(writer io.Writer) outputFormat {
	if state := outputContextFrom(writer); state != nil {
		return state.format
	}
	return outputText
}

func structuredOutput(writer io.Writer) bool {
	return outputFormatFor(writer) != outputText
}

func verboseOutput(writer io.Writer) bool {
	state := outputContextFrom(writer)
	return state != nil && state.verbose
}

// enableVerbose turns on diagnostics after argument preparation, for the
// forms only Cobra sees: a combined shorthand such as -nv, or a repeated -vv.
func enableVerbose(writer io.Writer) {
	if state := outputContextFrom(writer); state != nil {
		state.verbose = true
	}
}

func parseBooleanOption(argument, name string) (matched, enabled bool, err error) {
	if argument == name {
		return true, true, nil
	}
	prefix := name + "="
	if !strings.HasPrefix(argument, prefix) {
		return false, false, nil
	}
	switch strings.TrimPrefix(argument, prefix) {
	case "true":
		return true, true, nil
	case "false":
		return true, false, nil
	default:
		return true, false, fmt.Errorf("%s accepts only true or false", name)
	}
}

func rejectedPresentationAlias(argument string) bool {
	for _, alias := range []string{"-json", "-yaml", "-verbose", "--yml", "-yml"} {
		if argument == alias || strings.HasPrefix(argument, alias+"=") {
			return true
		}
	}
	return false
}

// environmentDefaults reads the two presentation environment variables that
// command-line flags override: FARROW_OUTPUT (text, json, yaml) and
// FARROW_VERBOSE (a boolean).
func environmentDefaults() (outputFormat, bool, error) {
	format := outputText
	if value, set := os.LookupEnv("FARROW_OUTPUT"); set && strings.TrimSpace(value) != "" {
		format = outputFormat(strings.ToLower(strings.TrimSpace(value)))
		switch format {
		case outputText, outputJSON, outputYAML:
		default:
			return "", false, fmt.Errorf("FARROW_OUTPUT must be text, json, or yaml, got %q", format)
		}
	}
	verbose := false
	if value, set := os.LookupEnv("FARROW_VERBOSE"); set && strings.TrimSpace(value) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return "", false, fmt.Errorf("FARROW_VERBOSE must be true or false, got %q", value)
		}
		verbose = parsed
	}
	return format, verbose, nil
}

// prepareOutput removes the cross-command presentation flags before the
// existing command-local flag parsers run. A literal -- ends presentation flag
// parsing so remote command arguments remain byte-for-byte untouched.
func prepareOutput(args []string, stdout, stderr io.Writer) ([]string, io.Writer, io.Writer, error) {
	clean := make([]string, 0, len(args))
	format, verbose, err := environmentDefaults()
	if err != nil {
		return nil, stdout, stderr, err
	}
	formatFromCLI := false
	passthrough := false
	for _, argument := range args {
		if argument == "--" {
			passthrough = true
			clean = append(clean, argument)
			continue
		}
		if passthrough {
			clean = append(clean, argument)
			continue
		}
		if rejectedPresentationAlias(argument) {
			return nil, stdout, stderr, fmt.Errorf("unsupported presentation flag %q; use --json, --yaml, or --verbose", argument)
		}
		if matched, enabled, err := parseBooleanOption(argument, "--json"); matched {
			if err != nil {
				return nil, stdout, stderr, err
			}
			if enabled {
				if formatFromCLI && format == outputYAML {
					return nil, stdout, stderr, errors.New("--json conflicts with --yaml")
				}
				format = outputJSON
				formatFromCLI = true
			} else if format == outputJSON {
				format = outputText
				formatFromCLI = true
			}
			continue
		}
		if matched, enabled, err := parseBooleanOption(argument, "--yaml"); matched {
			if err != nil {
				return nil, stdout, stderr, err
			}
			if enabled {
				if formatFromCLI && format == outputJSON {
					return nil, stdout, stderr, errors.New("--yaml conflicts with --json")
				}
				format = outputYAML
				formatFromCLI = true
			} else if format == outputYAML {
				format = outputText
				formatFromCLI = true
			}
			continue
		}
		if matched, enabled, err := parseBooleanOption(argument, "--verbose"); matched {
			if err != nil {
				return nil, stdout, stderr, err
			}
			verbose = enabled
			continue
		}
		if matched, enabled, err := parseBooleanOption(argument, "-v"); matched {
			if err != nil {
				return nil, stdout, stderr, err
			}
			verbose = enabled
			continue
		}
		clean = append(clean, argument)
	}
	_, stderrFile := writerFile(stderr)
	state := &outputContext{
		format:     format,
		verbose:    verbose,
		stdoutTTY:  writerTTY(stdout),
		stderrTTY:  writerTTY(stderr),
		stderrFile: stderrFile,
		color:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
	}
	return clean, &outputWriter{Writer: stdout, context: state}, &outputWriter{Writer: stderr, context: state, stderr: true}, nil
}

func normalizeForYAML(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized yaml.Node
	if err := yaml.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	clearYAMLFlowStyle(&normalized)
	return &normalized, nil
}

func clearYAMLFlowStyle(node *yaml.Node) {
	if node == nil {
		return
	}
	node.Style = 0
	for _, child := range node.Content {
		clearYAMLFlowStyle(child)
	}
}

func encodeOutput(out io.Writer, value any) error {
	var buffer bytes.Buffer
	switch outputFormatFor(out) {
	case outputYAML:
		normalized, err := normalizeForYAML(value)
		if err != nil {
			return err
		}
		encoder := yaml.NewEncoder(&buffer)
		encoder.SetIndent(2)
		if err := encoder.Encode(normalized); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
	default:
		encoder := json.NewEncoder(&buffer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	_, err := io.Copy(out, &buffer)
	return err
}

// encodeStreamOutput writes one self-contained stream record. JSON streams are
// NDJSON; YAML streams use explicit document separators. Finite commands use
// encodeOutput and remain a single document.
func encodeStreamOutput(out io.Writer, value any) error {
	var buffer bytes.Buffer
	switch outputFormatFor(out) {
	case outputYAML:
		normalized, err := normalizeForYAML(value)
		if err != nil {
			return err
		}
		buffer.WriteString("---\n")
		encoder := yaml.NewEncoder(&buffer)
		encoder.SetIndent(2)
		if err := encoder.Encode(normalized); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
	default:
		if err := json.NewEncoder(&buffer).Encode(value); err != nil {
			return err
		}
	}
	_, err := io.Copy(out, &buffer)
	return err
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

func writerColor(writer io.Writer) bool {
	contextual, ok := writer.(*outputWriter)
	if !ok || contextual.context == nil || !contextual.context.color {
		return false
	}
	if contextual.stderr {
		return contextual.context.stderrTTY
	}
	return contextual.context.format == outputText && contextual.context.stdoutTTY
}

func styled(writer io.Writer, style, text string) string {
	if !writerColor(writer) {
		return text
	}
	return style + text + ansiReset
}

func debugf(stderr io.Writer, format string, arguments ...any) {
	if !verboseOutput(stderr) {
		return
	}
	message := fmt.Sprintf(format, arguments...)
	fmt.Fprintf(stderr, "%s %s\n", styled(stderr, ansiDim, "debug:"), message)
}

func warningf(stderr io.Writer, format string, arguments ...any) {
	message := strings.TrimSpace(fmt.Sprintf(format, arguments...))
	message = strings.TrimSpace(strings.TrimPrefix(message, "WARNING:"))
	message = strings.TrimSpace(strings.TrimPrefix(message, "warning:"))
	fmt.Fprintf(stderr, "%s %s\n", styled(stderr, ansiYellow, "warning:"), message)
}

func errorf(stderr io.Writer, format string, arguments ...any) {
	message := strings.TrimSpace(fmt.Sprintf(format, arguments...))
	message = strings.TrimSpace(strings.TrimPrefix(message, "ERROR:"))
	message = strings.TrimSpace(strings.TrimPrefix(message, "error:"))
	recordCommandError(stderr, message)
	fmt.Fprintf(stderr, "%s %s\n", styled(stderr, ansiRed, "error:"), message)
}

func textField(writer io.Writer, width int, label string, value any) {
	label = strings.TrimSuffix(label, ":") + ":"
	padded := fmt.Sprintf("%-*s", width, label)
	fmt.Fprintf(writer, "%s %v\n", styled(writer, ansiDim, padded), value)
}

func statusStyle(value string) string {
	lower := strings.ToLower(value)
	style := ansiCyan
	switch {
	case lower == "ok", lower == "ready", lower == "running", lower == "supported", lower == "exact", lower == "healthy", lower == "valid", lower == "active", strings.HasPrefix(lower, "success"):
		style = ansiGreen
	case lower == "error", lower == "failed", lower == "invalid", lower == "unhealthy", lower == "conflict", strings.Contains(lower, "error"):
		style = ansiRed
	case lower == "warn", lower == "warning", lower == "stopped", lower == "degraded", lower == "unsupported", lower == "partial", lower == "blocked":
		style = ansiYellow
	}
	return style
}

func statusValue(writer io.Writer, value string) string {
	return styled(writer, statusStyle(value), value)
}

func statusCell(writer io.Writer, width int, value string) string {
	return styled(writer, statusStyle(value), fmt.Sprintf("%-*s", width, value))
}

type progress struct {
	stderr       io.Writer
	summary      string
	message      string
	started      time.Time
	cancel       context.CancelFunc
	done         chan struct{}
	once         sync.Once
	enabled      bool
	verbose      bool
	tty          bool
	mu           sync.Mutex
	phase        string
	last         time.Time
	currentBytes int64
	totalBytes   int64
	byteProgress bool
}

const terminalProgressBarWidth = 20

// progressBar is determinate when a byte total exists and a bouncing segment
// otherwise. The indeterminate form never invents a percentage for package
// managers such as Homebrew, which do not expose a stable total-work API.
func progressBar(current, total int64, width int, elapsed time.Duration) string {
	if width < 4 {
		width = 4
	}
	if total > 0 {
		if current < 0 {
			current = 0
		}
		if current > total {
			current = total
		}
		filled := int(float64(current) / float64(total) * float64(width))
		if filled >= width {
			return "[" + strings.Repeat("=", width) + "]"
		}
		return "[" + strings.Repeat("=", filled) + ">" + strings.Repeat(".", width-filled-1) + "]"
	}
	segment := 4
	travel := width - segment
	step := int(elapsed / (250 * time.Millisecond))
	cycle := travel * 2
	position := 0
	if cycle > 0 {
		position = step % cycle
		if position > travel {
			position = cycle - position
		}
	}
	return "[" + strings.Repeat(".", position) + strings.Repeat("=", segment) + strings.Repeat(".", width-position-segment) + "]"
}

// liveTTYMessage must be called while item.mu is held once the progress
// goroutine has started.
func (item *progress) liveTTYMessage(now time.Time) string {
	current, total := int64(0), int64(0)
	if item.byteProgress {
		current, total = item.currentBytes, item.totalBytes
	}
	elapsed := now.Sub(item.started)
	return fmt.Sprintf("%s %s %s", progressBar(current, total, terminalProgressBarWidth, elapsed), item.message, styled(item.stderr, ansiDim, elapsed.Round(time.Second).String()))
}

func progressBytes(value int64) string {
	if value < 0 {
		value = 0
	}
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	index := -1
	for amount >= 1024 && index < len(units)-1 {
		amount /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", amount, units[index])
}

func progressSource(source string) string {
	parsed, err := url.Parse(source)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		source = parsed.String()
	}
	return strings.TrimSpace(source)
}

func formatActivity(event activity.Event, now time.Time) string {
	message := strings.TrimSpace(event.Message)
	if source := progressSource(event.Source); source != "" {
		message += " from " + source
	}
	if event.TotalBytes > 0 || event.CurrentBytes > 0 {
		parts := make([]string, 0, 4)
		if event.TotalBytes > 0 {
			parts = append(parts, fmt.Sprintf("%s / %s (%.1f%%)", progressBytes(event.CurrentBytes), progressBytes(event.TotalBytes), 100*float64(event.CurrentBytes)/float64(event.TotalBytes)))
		} else {
			parts = append(parts, progressBytes(event.CurrentBytes))
		}
		if !event.StartedAt.IsZero() {
			elapsed := now.Sub(event.StartedAt)
			if elapsed >= 500*time.Millisecond && event.CurrentBytes > 0 {
				rate := float64(event.CurrentBytes) / elapsed.Seconds()
				parts = append(parts, progressBytes(int64(rate))+"/s")
				if !event.Done && event.TotalBytes > event.CurrentBytes && rate > 0 {
					eta := time.Duration(float64(event.TotalBytes-event.CurrentBytes) / rate * float64(time.Second))
					parts = append(parts, "ETA "+eta.Round(time.Second).String())
				}
			}
		}
		message += " — " + strings.Join(parts, " · ")
	}
	return strings.TrimSpace(message)
}

// Report updates the visible lifecycle stage. Fast byte updates are throttled
// while retaining the newest value for the regular elapsed-time repaint.
func (item *progress) Report(event activity.Event) {
	if item == nil || !item.enabled {
		return
	}
	now := time.Now()
	message := formatActivity(event, now)
	if message == "" {
		return
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	byteUpdate := event.TotalBytes > 0 || event.CurrentBytes > 0
	if byteUpdate {
		item.currentBytes = event.CurrentBytes
		item.totalBytes = event.TotalBytes
		item.byteProgress = true
	} else if event.Phase != item.phase {
		item.currentBytes = 0
		item.totalBytes = 0
		item.byteProgress = false
	}
	interval := 5 * time.Second
	if item.tty {
		interval = 250 * time.Millisecond
	}
	if byteUpdate && !event.Done && item.phase == event.Phase && !item.last.IsZero() && now.Sub(item.last) < interval {
		item.message = message
		return
	}
	item.message = message
	item.phase = event.Phase
	item.last = now
	marker := styled(item.stderr, ansiCyan, "→")
	if event.Done {
		marker = styled(item.stderr, ansiGreen, "✓")
	}
	if !item.tty {
		fmt.Fprintf(item.stderr, "%s %s\n", marker, message)
		return
	}
	if event.Done {
		// A completed phase persists as a checklist row; the live line
		// falls back to the overall command summary until the next phase.
		fmt.Fprintf(item.stderr, "\r\x1b[2K%s %s\n", marker, message)
		item.message = item.summary
		item.currentBytes = 0
		item.totalBytes = 0
		item.byteProgress = false
		return
	}
	fmt.Fprintf(item.stderr, "\r\x1b[2K%s %s", marker, item.liveTTYMessage(now))
}

// tickf persists an already-satisfied step as a completed checklist row, so a
// healthy repeat run still renders the full checklist.
func tickf(stderr io.Writer, format string, arguments ...any) {
	state := outputContextFrom(stderr)
	if state == nil || (!state.stderrFile && !state.verbose) {
		return
	}
	fmt.Fprintf(stderr, "%s %s\n", styled(stderr, ansiGreen, "✓"), fmt.Sprintf(format, arguments...))
}

func deferredProgressReporter(item **progress) activity.Reporter {
	return func(event activity.Event) {
		if item != nil && *item != nil {
			(*item).Report(event)
		}
	}
}

func startProgress(parent context.Context, stderr io.Writer, message string) *progress {
	state := outputContextFrom(stderr)
	enabled := state != nil && (state.stderrFile || state.verbose)
	item := &progress{
		stderr: stderr, summary: message, message: message, started: time.Now(), done: make(chan struct{}),
		enabled: enabled, verbose: state != nil && state.verbose, tty: state != nil && state.stderrTTY && state.color,
	}
	if !enabled {
		close(item.done)
		return item
	}
	ctx, cancel := context.WithCancel(parent)
	item.cancel = cancel
	if item.tty {
		fmt.Fprintf(stderr, "%s %s", styled(stderr, ansiCyan, "→"), item.liveTTYMessage(item.started))
	} else {
		fmt.Fprintf(stderr, "%s %s\n", styled(stderr, ansiCyan, "→"), message)
	}
	interval := time.Minute
	if item.tty {
		interval = 250 * time.Millisecond
	} else if item.verbose {
		interval = 15 * time.Second
	}
	go func() {
		defer close(item.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				elapsed := now.Sub(item.started).Round(time.Second)
				item.mu.Lock()
				if item.tty {
					fmt.Fprintf(stderr, "\r\x1b[2K%s %s", styled(stderr, ansiCyan, "→"), item.liveTTYMessage(now))
				} else {
					fmt.Fprintf(stderr, "%s %s (%s elapsed)\n", styled(stderr, ansiDim, "·"), item.message, elapsed)
				}
				item.mu.Unlock()
			}
		}
	}()
	return item
}

func (item *progress) Stop(err error) {
	if item == nil || !item.enabled {
		return
	}
	item.once.Do(func() {
		item.cancel()
		<-item.done
		item.mu.Lock()
		defer item.mu.Unlock()
		status := styled(item.stderr, ansiGreen, "✓")
		if err != nil {
			status = styled(item.stderr, ansiRed, "!")
		}
		if item.tty {
			fmt.Fprint(item.stderr, "\r\x1b[2K")
		}
		fmt.Fprintf(item.stderr, "%s %s (%s)\n", status, item.summary, time.Since(item.started).Round(time.Millisecond))
	})
}

func lifecycleMessage(command string) string {
	switch command {
	case "up":
		return "Preparing, starting, and configuring SSH for the deployment"
	case "start":
		return "Starting the deployment"
	case "stop":
		return "Stopping the deployment"
	case "restart":
		return "Restarting the deployment"
	case "reload":
		return "Stopping, re-reading the inventory, and converging the deployment"
	case "recreate":
		return "Recreating the deployment"
	case "destroy":
		return "Destroying owned deployment resources"
	default:
		return "Running " + command
	}
}
