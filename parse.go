// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/rauny-brandao/jig/manifest"
)

// Parse decodes a YAML manifest. Keys jig does not know are ignored, so a
// manifest can carry settings for the program embedding jig. Parse does not
// validate; call Manifest.Validate before installing.
func Parse(data []byte) (*manifest.Manifest, error) {
	var m manifest.Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Tools == nil {
		m.Tools = map[string]manifest.Tool{}
	}
	return &m, nil
}
