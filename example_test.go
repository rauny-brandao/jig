// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/host"
	"github.com/rauny-brandao/jig/kinds"
	"github.com/rauny-brandao/jig/manifest"
)

// Install every tool and extra in a manifest that is missing or outdated.
func Example() {
	ctx := context.Background()
	data, err := os.ReadFile("tools.yaml")
	if err != nil {
		log.Fatal(err)
	}
	m, err := jig.Parse(data)
	if err != nil {
		log.Fatal(err)
	}

	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".mytool")
	dirs := jig.Dirs{Base: root, Bin: filepath.Join(root, "bin"), Cache: filepath.Join(root, "cache"), Home: home}
	j, err := jig.New(host.Deps(), dirs, manifest.Current(), jig.WithHandlers(kinds.Defaults()...))
	if err != nil {
		log.Fatal(err)
	}
	if err := j.Validate(m); err != nil {
		log.Fatal(err)
	}

	statuses, err := j.Status(ctx, m)
	if err != nil {
		log.Fatal(err)
	}
	plan := j.Plan(statuses, jig.PlanOptions{})
	results, err := j.Apply(ctx, plan, func(e jig.Event) {
		fmt.Println(e.Type, e.Action.Op, e.Action.Name, e.Action.To)
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := results.Err(); err != nil {
		log.Fatal(err)
	}
}
