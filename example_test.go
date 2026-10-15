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
	"github.com/rauny-brandao/jig/jigtest"
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

// Narrow a plan with filters. The statuses stand in for what Status returns.
func ExampleJig_Plan_select() {
	j, err := jig.New(jigtest.NewEnv().Deps(), jig.Dirs{Base: "jig", Bin: "jig/bin", Cache: "jig/cache"},
		manifest.Platform{OS: "linux", Arch: "amd64"})
	if err != nil {
		log.Fatal(err)
	}
	statuses := []jig.Status{
		{Name: "gh", Kind: jig.KindTool, State: jig.StateMissing, Want: "2.102.0"},
		{Name: "jq", Kind: jig.KindTool, State: jig.StateOutdated, Have: "1.7.1", Want: "1.8.2"},
		{Name: "branch", Kind: "gh-extension", State: jig.StateMissing,
			Extra: manifest.Extra{Requires: []string{"gh"}}},
		{Name: "node", Kind: "node-runtime", State: jig.StateMissing},
	}

	fmt.Println("tools:")
	printPlan(j.Plan(statuses, jig.PlanOptions{Select: jig.OfKind(jig.KindTool)}))
	fmt.Println("named:")
	printPlan(j.Plan(statuses, jig.PlanOptions{Select: jig.Named("jq", "node")}))
	fmt.Println("custom:")
	printPlan(j.Plan(statuses, jig.PlanOptions{Select: func(s jig.Status) bool {
		return s.Kind == jig.KindTool || s.Name == "branch"
	}}))
	fmt.Println("nothing:")
	printPlan(j.Plan(statuses, jig.PlanOptions{Select: jig.Named()}))
	// Output:
	// tools:
	// install gh
	// upgrade jq 1.7.1 -> 1.8.2
	// named:
	// upgrade jq 1.7.1 -> 1.8.2
	// install node
	// custom:
	// install gh
	// upgrade jq 1.7.1 -> 1.8.2
	// install branch
	// nothing:
}

// Hold some items to their exact pin: versions newer than the pin are left
// alone unless Downgrade selects them.
func ExampleJig_Plan_downgrade() {
	j, err := jig.New(jigtest.NewEnv().Deps(), jig.Dirs{Base: "jig", Bin: "jig/bin", Cache: "jig/cache"},
		manifest.Platform{OS: "linux", Arch: "amd64"})
	if err != nil {
		log.Fatal(err)
	}
	statuses := []jig.Status{
		{Name: "gh", Kind: jig.KindTool, State: jig.StateNewer, Have: "2.110.0", Want: "2.102.0"},
		{Name: "semver", Kind: "npm-global", State: jig.StateNewer, Have: "7.9.0", Want: "7.8.5"},
	}

	fmt.Println("default:")
	printPlan(j.Plan(statuses, jig.PlanOptions{}))
	fmt.Println("exact npm pins:")
	printPlan(j.Plan(statuses, jig.PlanOptions{Downgrade: jig.OfKind("npm-global")}))
	// Output:
	// default:
	// exact npm pins:
	// downgrade semver 7.9.0 -> 7.8.5
}

func printPlan(plan jig.Plan) {
	for _, a := range plan.Actions {
		if a.From != "" {
			fmt.Println(a.Op, a.Name, a.From, "->", a.To)
		} else {
			fmt.Println(a.Op, a.Name)
		}
	}
}
