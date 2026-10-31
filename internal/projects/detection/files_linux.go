//go:build linux

package detection

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	maxFileBytes = 512 << 10
	maxScanBytes = 8 << 20
	maxFiles     = 256
	maxEntries   = 2048
	maxDepth     = 4
)

var ErrDirectory = errors.New("project directory unavailable")

type reader struct {
	ctx                   context.Context
	root                  *os.File
	report                *Report
	cache                 map[string][]byte
	loaded                map[string]bool
	seenWarnings          map[string]bool
	bytes, files, entries int
}

func openReader(ctx context.Context, path string, report *Report) (*reader, error) {
	if !utf8.ValidString(path) || len(path) > 4096 || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, ErrDirectory
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return nil, ErrDirectory
	}
	root, err := os.OpenFile(canonical, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrDirectory
	}
	info, err := root.Stat()
	if err != nil || !info.IsDir() {
		root.Close()
		return nil, ErrDirectory
	}
	report.Path = canonical
	return &reader{ctx: ctx, root: root, report: report, cache: map[string][]byte{}, loaded: map[string]bool{}, seenWarnings: map[string]bool{}}, nil
}

// Linux openat2 pins resolution beneath the selected directory and rejects all
// intervening/final symlinks. Nonblocking opens prevent FIFO metadata from hanging.
// An unsupported kernel fails closed rather than using a weaker path fallback.
func (r *reader) open(name string) (*os.File, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat2(int(r.root.Fd()), name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (r *reader) warn(code, file, message string) {
	key := code + ":" + file
	if r.seenWarnings[key] {
		return
	}
	r.seenWarnings[key] = true
	if len(r.report.Findings) >= 64 {
		r.report.Partial = true
		return
	}
	r.report.Findings = append(r.report.Findings, Finding{code, file, message})
}
func (r *reader) limit(file string) {
	r.report.Partial = true
	r.warn("scan_limit", file, "Inspection reached a size, complexity, file, depth or entry limit. Results may be incomplete; inspect a smaller project directory.")
}
func (r *reader) unavailable(name string, err error) {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	r.report.Partial = true
	r.warn("unreadable", name, "This entry could not be inspected. Symlinks, special files and inaccessible metadata are not followed.")
}

func (r *reader) present(name string) bool {
	file, err := r.open(name)
	if err != nil {
		r.unavailable(name, err)
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		r.unavailable(name, ErrDirectory)
		return false
	}
	return true
}

func (r *reader) read(name string) ([]byte, bool) {
	if r.loaded[name] {
		data, ok := r.cache[name]
		return data, ok
	}
	r.loaded[name] = true
	if r.files >= maxFiles || r.bytes >= maxScanBytes {
		r.limit(name)
		return nil, false
	}
	file, err := r.open(name)
	if err != nil {
		r.unavailable(name, err)
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		r.unavailable(name, ErrDirectory)
		return nil, false
	}
	if info.Size() > maxFileBytes {
		r.limit(name)
		return nil, false
	}
	remaining := min(maxFileBytes, maxScanBytes-r.bytes)
	data, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
	r.files++
	r.bytes += len(data)
	if err != nil {
		r.unavailable(name, err)
		return nil, false
	}
	if len(data) > remaining {
		r.limit(name)
		return nil, false
	}
	if !utf8.Valid(data) || slices.Contains(data, byte(0)) {
		r.report.Partial = true
		r.warn("invalid_text", name, "Metadata is not valid UTF-8 text without NUL. It was not interpreted.")
		return nil, false
	}
	if slices.Contains([]string{".toml", ".yaml", ".yml"}, filepath.Ext(name)) && !boundedStructure(data) {
		r.limit(name)
		return nil, false
	}
	r.cache[name] = data
	return data, true
}

// A conservative lexical complexity budget runs before recursive TOML/YAML
// parsing. Delimiters in quoted values count too; unusually complex valid text
// may be refused. This guard is not used to interpret either syntax.
func boundedStructure(data []byte) bool {
	depth, dots, punctuation, indent := 0, 0, 0, 0
	leading := true
	for _, c := range data {
		if c == '\n' {
			dots, punctuation, indent, leading = 0, 0, 0, true
			continue
		}
		if leading && (c == ' ' || c == '\t') {
			indent++
			if indent > 128 {
				return false
			}
		} else {
			leading = false
		}
		switch c {
		case '[', '{':
			depth++
			if depth > 64 {
				return false
			}
		case ']', '}':
			depth = max(0, depth-1)
		case '.':
			dots++
			if dots > 128 {
				return false
			}
		}
		if slices.Contains([]byte{'[', '{', ']', '}', '-', ':', '.'}, c) {
			punctuation++
			if punctuation > 1024 {
				return false
			}
		}
	}
	return true
}

func (r *reader) directory(name string) []os.DirEntry {
	if r.entries >= maxEntries {
		r.limit(name)
		return nil
	}
	file, err := r.open(name)
	if err != nil {
		r.unavailable(name, err)
		return nil
	}
	defer file.Close()
	entries, err := file.ReadDir(maxEntries - r.entries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		r.unavailable(name, err)
		return nil
	}
	if len(entries) > maxEntries-r.entries {
		entries = entries[:maxEntries-r.entries]
		r.limit(name)
	}
	r.entries += len(entries)
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries
}

func readableName(name string) bool {
	if len(name) > 512 || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
