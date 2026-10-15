// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package kinds

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

// JDK installs a Java Development Kit with Coursier (cs). The extra's name is
// the JVM selector, such as "temurin:25", unless the jvm field overrides it.
// Fields:
//
//	jvm  the Coursier JVM selector (optional; defaults to the name)
//
// Removal deletes the JDK's directory, but only when it lies inside
// Coursier's own cache. For a JDK unpacked from the archive cache it also
// deletes the downloaded archive, which Coursier would otherwise unpack again.
// A JDK elsewhere is left in place with an error that wraps
// jig.ErrNotManaged.
type JDK struct{}

var _jdkFields = fields{optional: []string{"jvm"}}

// Kind implements jig.Handler.
func (JDK) Kind() jig.Kind { return "jdk" }

// Validate implements jig.Handler.
func (JDK) Validate(e manifest.Extra) error { return _jdkFields.validate(e) }

// Detect implements jig.Handler. `cs java-home` downloads a missing JVM, so it
// runs in offline mode: a JVM that is not already cached is reported as not
// installed instead of being fetched.
func (JDK) Detect(ctx context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	found := map[string]jig.Detected{}
	for _, e := range extras {
		result, err := env.Probe(ctx, env.Command("cs"), "java-home", "--jvm", jvmSelector(e), "--mode", "offline")
		if err != nil {
			found[e.Name] = jig.Detected{}
			continue
		}
		home := firstLine(result.Stdout)
		found[e.Name] = jig.Detected{Installed: home != "", Location: home}
	}
	return found, nil
}

// Install implements jig.Handler; `cs java-home` downloads the JVM when it is
// missing.
func (JDK) Install(ctx context.Context, env jig.Env, e manifest.Extra) error {
	_, err := env.Mutate(ctx, nil, env.Command("cs"), "java-home", "--jvm", jvmSelector(e))
	return err
}

// Remove implements jig.Handler. Coursier has no uninstall command, so the
// JDK directory is deleted after confirming it belongs to Coursier's cache.
func (h JDK) Remove(ctx context.Context, env jig.Env, e manifest.Extra) error {
	d, err := detectOne(ctx, h, env, e)
	if err != nil {
		return err
	}
	if !d.Installed {
		return fmt.Errorf("%s: %w", e.Name, jig.ErrNotInstalled)
	}
	dir, download, err := coursierJDKDir(env, d.Location)
	if err != nil {
		return fmt.Errorf("remove JDK %s: %w", e.Name, err)
	}
	if err := env.FS.Remove(dir); err != nil {
		return fmt.Errorf("remove JDK %s at %s: %w", e.Name, dir, err)
	}
	if download == "" {
		return nil
	}
	// Coursier marks a verified download with an empty ".<name>.checked" file.
	checked := filepath.Join(filepath.Dir(download), "."+filepath.Base(download)+".checked")
	for _, p := range []string{download, checked} {
		if err := env.FS.Remove(p); err != nil {
			return fmt.Errorf("remove JDK %s download %s: %w", e.Name, p, err)
		}
	}
	return nil
}

func jvmSelector(e manifest.Extra) string {
	if jvm := e.Param("jvm"); jvm != "" {
		return jvm
	}
	return e.Name
}

// coursierJDKDir maps a JAVA_HOME reported by Coursier to the one directory
// that holds that JDK: the first directory below the JVM cache, or the
// archive-named directory inside the archive cache. For the archive cache it
// also returns the downloaded archive, which Coursier's download cache keeps
// at the same relative path. Anything else is refused.
func coursierJDKDir(env jig.Env, javaHome string) (dir, download string, err error) {
	jvmCache, err := coursierCache(env, "COURSIER_JVM_CACHE", "jvm")
	if err != nil {
		return "", "", err
	}
	if dir, ok := firstBelow(env, jvmCache, javaHome); ok {
		return dir, "", nil
	}
	arcCache, err := coursierCache(env, "COURSIER_ARCHIVE_CACHE", "arc")
	if err != nil {
		return "", "", err
	}
	if rel, ok := archiveAncestor(env, arcCache, javaHome); ok {
		downloads, err := coursierCache(env, "COURSIER_CACHE", "v1")
		if err != nil {
			return "", "", err
		}
		return filepath.Join(arcCache, rel), filepath.Join(downloads, rel), nil
	}
	return "", "", fmt.Errorf("java home %s is not inside Coursier's caches (%s, %s): %w", javaHome, jvmCache, arcCache, jig.ErrNotManaged)
}

// coursierCache returns one of Coursier's cache directories, honouring its
// environment override and otherwise using the platform default.
func coursierCache(env jig.Env, envKey, name string) (string, error) {
	if override := strings.TrimSpace(env.Getenv(envKey)); override != "" {
		return override, nil
	}
	if env.Dirs.Home == "" {
		return "", errors.New("home directory unknown: set jig.Dirs.Home")
	}
	switch env.Platform.OS {
	case "darwin":
		return filepath.Join(env.Dirs.Home, "Library", "Caches", "Coursier", name), nil
	case "windows":
		if local := strings.TrimSpace(env.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "Coursier", "Cache", name), nil
		}
		return filepath.Join(env.Dirs.Home, "AppData", "Local", "Coursier", "Cache", name), nil
	default:
		return filepath.Join(env.Dirs.Home, ".cache", "coursier", name), nil
	}
}

// firstBelow returns the first path element of target below root, when
// target is strictly inside root.
func firstBelow(env jig.Env, root, target string) (string, bool) {
	rel, ok := relativeInside(env, root, target)
	if !ok {
		return "", false
	}
	first, _, _ := strings.Cut(rel, "/")
	return filepath.Join(root, first), true
}

// archiveAncestor returns, relative to root, the nearest ancestor of target
// below root whose name is an archive file name (Coursier unpacks each archive
// into a directory named after it).
func archiveAncestor(env jig.Env, root, target string) (string, bool) {
	rel, ok := relativeInside(env, root, target)
	if !ok {
		return "", false
	}
	for current := rel; current != "." && current != ""; current = path.Dir(current) {
		if isArchiveName(path.Base(current)) {
			return filepath.FromSlash(current), true
		}
	}
	return "", false
}

// relativeInside returns target relative to root, slash-separated, when
// target is strictly inside root. Windows paths compare case-insensitively.
func relativeInside(env jig.Env, root, target string) (string, bool) {
	r, t := slashClean(root), slashClean(target)
	cr, ct := r, t
	if env.Platform.OS == "windows" {
		cr, ct = strings.ToLower(r), strings.ToLower(t)
	}
	if !strings.HasPrefix(ct, cr+"/") {
		return "", false
	}
	rel := t[len(r)+1:]
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}
	return rel, true
}

func slashClean(p string) string {
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

func isArchiveName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".zip")
}
