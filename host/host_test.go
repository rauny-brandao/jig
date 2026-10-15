// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestMain lets the test binary act as the command under test: when
// JIG_HOST_HELPER is set, it prints the requested output and exits with the
// requested code instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("JIG_HOST_HELPER") == "1" {
		args := os.Args[1:]
		code, _ := strconv.Atoi(args[0])
		_, _ = fmt.Fprint(os.Stdout, args[1])
		_, _ = fmt.Fprint(os.Stderr, args[2])
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestRunnerCapturesOutput(t *testing.T) {
	t.Setenv("JIG_HOST_HELPER", "1")
	result, err := Runner{}.Run(t.Context(), os.Args[0], "0", "out", "err")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 0 {
		t.Errorf("Run() = %+v", result)
	}
}

func TestRunnerReportsNonZeroExitAsResult(t *testing.T) {
	t.Setenv("JIG_HOST_HELPER", "1")
	result, err := Runner{}.Run(t.Context(), os.Args[0], "3", "", "failed")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil for a non-zero exit", err)
	}
	if result.ExitCode != 3 || result.Stderr != "failed" {
		t.Errorf("Run() = %+v, want exit 3", result)
	}
}

func TestRunnerReportsMissingCommand(t *testing.T) {
	_, err := Runner{}.Run(t.Context(), filepath.Join(t.TempDir(), "no-such-command"))
	if !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("Run() error = %v, want ErrCommandNotFound", err)
	}
}

func TestRunnerRunsBatchFilesOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files only exist on Windows")
	}
	script := filepath.Join(t.TempDir(), "tool.cmd")
	if err := os.WriteFile(script, []byte("@echo tool version %1\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Runner{}.Run(t.Context(), script, "1.2.3")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "tool version 1.2.3") {
		t.Errorf("Stdout = %q", result.Stdout)
	}
}

func TestWrapBatch(t *testing.T) {
	name, args := wrapBatch("windows", `C:\sdk\bin\tool.CMD`, []string{"version"})
	if name != "cmd.exe" || !slices.Equal(args, []string{"/c", `C:\sdk\bin\tool.CMD`, "version"}) {
		t.Errorf("windows .cmd = %q %q", name, args)
	}
	name, args = wrapBatch("windows", `C:\bin\tool.exe`, []string{"version"})
	if name != `C:\bin\tool.exe` || !slices.Equal(args, []string{"version"}) {
		t.Errorf("windows .exe = %q %q", name, args)
	}
	name, _ = wrapBatch("darwin", "/bin/tool.cmd", nil)
	if name != "/bin/tool.cmd" {
		t.Errorf("darwin = %q", name)
	}
}

func TestFS(t *testing.T) {
	dir := t.TempDir()
	fsys := FS{}
	nested := filepath.Join(dir, "a", "b")
	file := filepath.Join(nested, "file")

	if err := fsys.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fsys.Exists(file) {
		t.Fatal("Exists() = false after WriteFile")
	}
	moved := filepath.Join(dir, "moved")
	if err := fsys.Rename(filepath.Join(dir, "a"), moved); err != nil {
		t.Fatal(err)
	}
	if data, err := fsys.ReadFile(filepath.Join(moved, "b", "file")); err != nil || string(data) != "data" {
		t.Fatalf("ReadFile after Rename = %q, %v", data, err)
	}
	if err := fsys.Remove(moved); err != nil {
		t.Fatal(err)
	}
	if fsys.Exists(moved) {
		t.Error("Exists() = true after Remove")
	}
	if err := fsys.Remove(moved); err != nil {
		t.Errorf("Remove(missing) = %v, want nil", err)
	}
}
