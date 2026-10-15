// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command jig-checksums downloads every asset in a tools manifest and writes
// its SHA-256 into the manifest.
//
// Usage:
//
//	jig-checksums [-f tools.yaml] [-tool gh,uv] [-check] [-parallel 4] [-timeout 30m]
//
// With -check, nothing is written and the command exits 1 when any checksum
// differs, which suits a CI step that verifies a committed manifest.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/rauny-brandao/jig/checksums"
	"github.com/rauny-brandao/jig/fetch"
)

var errChecksumsDiffer = errors.New("checksums differ from the manifest")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stderr, fetch.New().HashURL)
	stop()
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "jig-checksums:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer, hash checksums.HashFunc) error {
	flags := flag.NewFlagSet("jig-checksums", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("f", "tools.yaml", "manifest `file` to update")
	tools := flags.String("tool", "", "comma-separated `names` of tools to update (default all)")
	check := flags.Bool("check", false, "report differences and exit 1 instead of writing")
	parallel := flags.Int("parallel", 4, "number of concurrent downloads")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall time limit")
	if err := flags.Parse(args); err != nil {
		return err
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	opts := checksums.Options{Parallelism: *parallel}
	if *tools != "" {
		opts.Tools = strings.Split(*tools, ",")
	}
	updated, changes, err := checksums.Update(ctx, data, hash, opts)
	if err != nil {
		return err
	}

	for _, c := range changes {
		_, _ = fmt.Fprintf(stderr, "%s/%s: %s -> %s\n", c.Tool, c.Platform, displaySum(c.Old), c.New)
	}
	switch {
	case len(changes) == 0:
		_, _ = fmt.Fprintln(stderr, "all checksums are up to date")
		return nil
	case *check:
		return fmt.Errorf("%d %w", len(changes), errChecksumsDiffer)
	}
	if err := writeFileAtomic(*file, updated); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "updated %d checksum(s) in %s\n", len(changes), *file)
	return nil
}

func displaySum(sum string) string {
	if sum == "" {
		return "(none)"
	}
	return sum
}

// writeFileAtomic replaces path with data, keeping its permissions, so an
// interrupted run never leaves a truncated manifest.
func writeFileAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jig-checksums-*")
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
