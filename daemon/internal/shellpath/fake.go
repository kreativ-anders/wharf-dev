package shellpath

import (
	"slices"
	"sync"
)

// Fake is a Registrar for tests: it remembers which folders are on PATH
// without touching a startup file or the user environment.
type Fake struct {
	// Places is what Add reports writing to; one startup file by default.
	Places []string
	// Err, when set, is returned by Add and Remove.
	Err error

	mu    sync.Mutex
	dirs  []string
	calls []string
}

// NewFake returns a Fake that reports writing to one startup file.
func NewFake() *Fake { return &Fake{Places: []string{"~/.zshrc"}} }

func (f *Fake) Add(dir string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "add "+dir)
	if f.Err != nil {
		return nil, f.Err
	}
	if !slices.Contains(f.dirs, dir) {
		f.dirs = append(f.dirs, dir)
	}
	return slices.Clone(f.Places), nil
}

func (f *Fake) Remove(dir string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "remove "+dir)
	if f.Err != nil {
		return nil, f.Err
	}
	if !slices.Contains(f.dirs, dir) {
		return []string{}, nil
	}
	f.dirs = slices.DeleteFunc(f.dirs, func(d string) bool { return d == dir })
	return slices.Clone(f.Places), nil
}

func (f *Fake) Where(dir string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slices.Contains(f.dirs, dir) {
		return slices.Clone(f.Places)
	}
	return []string{}
}

// OnPath reports whether dir is on the fake PATH.
func (f *Fake) OnPath(dir string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.dirs, dir)
}

// Calls lists every Add and Remove, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}
