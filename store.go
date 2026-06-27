package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	dateLayout  = "2006-01-02"     // internal map key
	humanLayout = "2 January 2006" // on-disk header, e.g. "26 June 2026"
)

// isoHeaderRe matches the legacy header style "## 2026-06-26 (Friday)".
var isoHeaderRe = regexp.MustCompile(`^#{1,2}\s+(\d{4}-\d{2}-\d{2})\b`)

// humanHeaderRe matches the current header style "# 26 June 2026".
var humanHeaderRe = regexp.MustCompile(`^#\s+(\d{1,2}\s+[A-Za-z]+\s+\d{4})\s*$`)

// parseHeader returns the day key (YYYY-MM-DD) for a header line, accepting
// both the human ("# 26 June 2026") and legacy ISO ("## 2026-06-26") styles.
func parseHeader(line string) (string, bool) {
	if m := humanHeaderRe.FindStringSubmatch(line); m != nil {
		if t, err := time.Parse(humanLayout, strings.Join(strings.Fields(m[1]), " ")); err == nil {
			return t.Format(dateLayout), true
		}
	}
	if m := isoHeaderRe.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	return "", false
}

// Store holds the contents of the journal keyed by day (YYYY-MM-DD).
type Store struct {
	path string
	days map[string]string
}

// LoadStore reads the markdown journal at path. A missing file is treated as
// an empty journal.
func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, days: map[string]string{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	var curKey string
	var buf []string
	flush := func() {
		if curKey != "" {
			// Drop empty sections (e.g. a header peeked at via "o" but never
			// filled in) so they don't linger in memory or get rewritten.
			if body := strings.Trim(strings.Join(buf, "\n"), "\n"); strings.TrimSpace(body) != "" {
				s.days[curKey] = body
			}
		}
		buf = buf[:0]
	}

	for _, line := range strings.Split(string(data), "\n") {
		if key, ok := parseHeader(line); ok {
			flush()
			curKey = key
			continue
		}
		if curKey != "" {
			buf = append(buf, line)
		}
	}
	flush()

	return s, nil
}

// Get returns the body text stored for the given day.
func (s *Store) Get(day time.Time) string {
	return s.days[day.Format(dateLayout)]
}

// Set replaces the body text for the given day.
func (s *Store) Set(day time.Time, body string) {
	key := day.Format(dateLayout)
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		delete(s.days, key)
		return
	}
	s.days[key] = body
}

// render builds the full markdown document, sorted by day. If ensure is
// non-empty that day is always included (with an empty body if it has no
// content yet) so callers can jump to it. It returns the document and the
// 1-based line number of the ensured day's header (0 if not ensured).
func (s *Store) render(ensure string) (string, int) {
	keySet := map[string]bool{}
	for k, v := range s.days {
		if strings.TrimSpace(v) != "" {
			keySet[k] = true
		}
	}
	if ensure != "" {
		keySet[ensure] = true // jumped-to day is written even if empty
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	line, headerLine := 1, 0
	for i, k := range keys {
		if i > 0 {
			b.WriteString("\n")
			line++
		}
		if k == ensure {
			headerLine = line
		}
		day, _ := time.Parse(dateLayout, k)
		section := fmt.Sprintf("# %s\n\n%s\n", day.Format(humanLayout), s.days[k])
		b.WriteString(section)
		line += strings.Count(section, "\n")
	}
	return b.String(), headerLine
}

func (s *Store) write(content string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, []byte(content), 0o644)
}

// Save writes the journal back to disk, sorted by day.
func (s *Store) Save() error {
	content, _ := s.render("")
	return s.write(content)
}

// WriteEnsuring writes the journal to disk guaranteeing that the given day has
// a header (creating an empty section if needed) and returns the 1-based line
// number of that header, suitable for "vim +N".
func (s *Store) WriteEnsuring(day time.Time) (int, error) {
	content, line := s.render(day.Format(dateLayout))
	if err := s.write(content); err != nil {
		return 0, err
	}
	return line, nil
}

// Path returns the on-disk location of the journal.
func (s *Store) Path() string { return s.path }

// Reload re-reads the journal from disk, discarding in-memory state.
func (s *Store) Reload() error {
	fresh, err := LoadStore(s.path)
	if err != nil {
		return err
	}
	s.days = fresh.days
	return nil
}
