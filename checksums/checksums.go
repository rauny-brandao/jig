// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package checksums refreshes the sha256 fields of a YAML tools manifest by
// downloading every asset. It edits the YAML node tree, so comments, key
// order and quoting elsewhere in the file are preserved.
package checksums

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/rauny-brandao/jig/manifest"
)

// HashFunc downloads url and returns the hex-encoded SHA-256 of its content.
type HashFunc func(ctx context.Context, url string) (string, error)

// Change is an asset whose checksum differs from the manifest.
type Change struct {
	Tool     string
	Platform string
	URL      string
	// Old is the previous checksum ("" when unset); New the computed one.
	Old, New string
}

// Options controls Update.
type Options struct {
	// Tools limits the update to the named tools. Empty means all.
	Tools []string
	// Parallelism is the number of concurrent downloads; below 1 means 4.
	Parallelism int
}

// Update recomputes the checksum of every asset in the YAML manifest data
// and returns the updated document with the checksums that changed. The
// document is returned unchanged, byte for byte, when nothing changed.
func Update(ctx context.Context, data []byte, hash HashFunc, opts Options) ([]byte, []Change, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, nil, errors.New("parse manifest: document is empty")
	}
	tools := mapValue(doc.Content[0], "tools")
	if tools == nil {
		return nil, nil, errors.New("parse manifest: no tools section")
	}

	jobs := collect(tools, opts.Tools)
	if err := hashAll(ctx, jobs, hash, opts.Parallelism); err != nil {
		return nil, nil, err
	}

	var changes []Change
	for _, job := range jobs {
		if job.change.New == job.change.Old {
			continue
		}
		setMapValue(job.asset, "sha256", job.change.New)
		changes = append(changes, job.change)
	}
	if len(changes) == 0 {
		return data, nil, nil
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, fmt.Errorf("encode manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, nil, fmt.Errorf("encode manifest: %w", err)
	}
	return out.Bytes(), changes, nil
}

type job struct {
	asset  *yaml.Node
	change Change
}

func collect(tools *yaml.Node, only []string) []*job {
	var jobs []*job
	for i := 0; i+1 < len(tools.Content); i += 2 {
		name, tool := tools.Content[i].Value, tools.Content[i+1]
		if len(only) > 0 && !slices.Contains(only, name) {
			continue
		}
		version := mapValue(tool, "version")
		platforms := mapValue(tool, "platforms")
		if version == nil || platforms == nil {
			continue
		}
		for j := 0; j+1 < len(platforms.Content); j += 2 {
			asset := platforms.Content[j+1]
			url := mapValue(asset, "url")
			if url == nil || url.Value == "" {
				continue
			}
			old := ""
			if sha := mapValue(asset, "sha256"); sha != nil {
				old = sha.Value
			}
			jobs = append(jobs, &job{asset: asset, change: Change{
				Tool:     name,
				Platform: platforms.Content[j].Value,
				URL:      strings.ReplaceAll(url.Value, manifest.VersionPlaceholder, version.Value),
				Old:      old,
			}})
		}
	}
	return jobs
}

func hashAll(ctx context.Context, jobs []*job, hash HashFunc, parallelism int) error {
	if parallelism < 1 {
		parallelism = 4
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make([]error, len(jobs))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			sum, err := hash(ctx, j.change.URL)
			if err != nil {
				errs[i] = fmt.Errorf("%s/%s: %w", j.change.Tool, j.change.Platform, err)
				cancel()
				return
			}
			j.change.New = sum
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("hash assets: %w", err)
	}
	return nil
}

func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMapValue(node *yaml.Node, key, value string) {
	if existing := mapValue(node, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = "!!str"
		existing.Value = value
		existing.Style = yaml.DoubleQuotedStyle
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle},
	)
}
