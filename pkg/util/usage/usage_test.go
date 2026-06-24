// SPDX-License-Identifier: AGPL-3.0-only

package usage

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

type testConfig struct {
	Visible string
	Other   string
	Hidden  string `doc:"hidden"`
}

func (c *testConfig) registerFlags(f *flag.FlagSet) {
	f.StringVar(&c.Visible, "visible-flag", "", "a visible flag")
	f.StringVar(&c.Other, "other-flag", "", "another visible flag")
	f.StringVar(&c.Hidden, "hidden-flag", "", "a hidden flag")
}

// withCommandLine swaps flag.CommandLine for a fresh flag set wired to buf,
// runs fn, and restores the original. Usage reads the global flag.CommandLine.
func withCommandLine(t *testing.T, cfg *testConfig, buf *bytes.Buffer, register func(*flag.FlagSet)) {
	t.Helper()
	orig := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = orig })

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(buf)
	cfg.registerFlags(fs)
	if register != nil {
		register(fs)
	}
	flag.CommandLine = fs

	if err := Usage(cfg); err != nil {
		t.Fatalf("Usage returned error: %v", err)
	}
}

func TestUsage_PrintsAllNonHiddenFlags(t *testing.T) {
	var cfg testConfig
	var buf bytes.Buffer
	withCommandLine(t, &cfg, &buf, nil)

	out := buf.String()
	for _, name := range []string{"-visible-flag", "-other-flag"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s should be printed:\n%s", name, out)
		}
	}
}

func TestUsage_OmitsHiddenFlags(t *testing.T) {
	var cfg testConfig
	var buf bytes.Buffer
	withCommandLine(t, &cfg, &buf, nil)

	if strings.Contains(buf.String(), "-hidden-flag") {
		t.Errorf("flags tagged doc:\"hidden\" should not be printed:\n%s", buf.String())
	}
}

// Flags registered against local variables (not the passed config structs) must
// still appear, so a new flag in libmain can't silently vanish from help.
func TestUsage_PrintsUnmappedFlags(t *testing.T) {
	var cfg testConfig
	var buf bytes.Buffer
	var local int
	withCommandLine(t, &cfg, &buf, func(fs *flag.FlagSet) {
		fs.IntVar(&local, "local-only-flag", 0, "a flag not bound to any config struct")
	})

	if !strings.Contains(buf.String(), "-local-only-flag") {
		t.Errorf("unmapped flags should still be printed:\n%s", buf.String())
	}
}

func TestUsage_RejectsNonPointerConfig(t *testing.T) {
	if err := Usage(testConfig{}); err == nil {
		t.Error("Usage should reject a non-pointer config")
	}
}
