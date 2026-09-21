package supervisor

import (
	"io"
	"os"
	"regexp"
	"strings"
)

// maxStartupOutput bounds how much of a log a failed start reads: a service
// that floods its log before dying must not stall the error report.
const maxStartupOutput = 64 << 10

// MaxLogSize is how large a log may grow before its service's next start
// moves it aside (project-logs.feature, "A log that has grown large starts
// afresh").
const MaxLogSize = 10 << 20

// trimLogs moves each log past MaxLogSize to "<log>.1", replacing the one
// there, so a log never grows without end but the latest lines survive a
// start. Best effort: a log that cannot be moved — on Windows, one another
// running instance still holds open — is left to grow until the next start.
func trimLogs(paths ...string) {
	for _, path := range paths {
		if path == "" || logSize(path) <= MaxLogSize {
			continue
		}
		_ = os.Rename(path, path+".1")
	}
}

// logSize is where a log ends before a run starts; zero when it has no log.
func logSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// nginxPrefix is the timestamp, level and pid nginx puts before every
// message. It says nothing the user needs, and it pushes the message itself
// out of view.
var nginxPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[\w+\] \d+#\d+: `)

// startupOutput is the message a service left in its log since offset: its
// last line, which is where nginx, Apache and php-fpm each put the reason they
// refused to start. Apache splits it — "Syntax error on line 17 of …:" then
// the complaint — so a line ending in a colon is kept with the one after it.
func startupOutput(path string, offset int64) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if size := logSize(path); size-offset > maxStartupOutput {
		offset = size - maxStartupOutput
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(f, maxStartupOutput))
	if err != nil {
		return ""
	}

	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	last := nginxPrefix.ReplaceAllString(lines[len(lines)-1], "")
	if len(lines) > 1 && strings.HasSuffix(lines[len(lines)-2], ":") {
		last = lines[len(lines)-2] + " " + last
	}
	return last
}
